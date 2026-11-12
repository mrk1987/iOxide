package protonmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/emersion/go-ical"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const calendarPath = "/calendar/v1"

type CalendarFlags int

type Calendar struct {
	ID         string
	Type       int
	CreateTime Timestamp
	Members    []CalendarMemberView
}

type CalendarBootstrap struct {
	Keys             []CalendarKey
	Passphrase       CalendarPassphrase
	Members          []CalendarMemberView
	CalendarSettings CalendarSettings
}

type CalendarSettings struct {
	ID                          string
	CalendarID                  string
	DefaultEventDuration        int
	DefaultPartDayNotifications []CalendarNotification
	DefaultFullDayNotifications []CalendarNotification
}

type CalendarKey struct {
	ID           string
	PrivateKey   string
	PassphraseID string
	Flags        int
	CalendarID   string
}

type CalendarPassphrase struct {
	Flags             int
	ID                string
	MemberPassphrases []CalendarMemberPassphrase
	CalendarID        string
}

type CalendarMemberView struct {
	ID          string
	Permissions int
	Email       string
	AddressID   string
	CalendarID  string
	Name        string
	Description string
	Color       string
	Display     int
	Priority    int
	Flags       int
}

type CalendarMemberPassphrase struct {
	MemberID   string
	Passphrase string
	Signature  string
}

type CalendarEventPermissions int

const (
	CalendarEventAvailability CalendarEventPermissions = 1 << iota
	CalendarEventRead
	CalendarEventReadMemberList
	CalendarEventWrite
	CalendarEventAdmin
	CalendarEventOwner
)

type CalendarEvent struct {
	ID                         string
	CalendarID                 string
	SharedEventID              string
	CalendarKeyPacket          string
	CreateTime, ModifyTime     Timestamp
	LastEditTime               Timestamp
	StartTime, EndTime         Timestamp
	StartTimezone, EndTimezone string
	FullDay                    int
	UID                        string
	IsOrganizer                int
	RecurrenceID               Timestamp
	Exdates                    []interface{}
	RRule                      interface{}
	Author                     string
	Permissions                CalendarEventPermissions
	SharedKeyPacket            string
	SharedEvents               []CalendarEventCard
	CalendarEvents             []CalendarEventCard
	PersonalEvents             []CalendarEventCard
	AttendeesEvents            []CalendarEventCard
	Attendees                  []interface{}
	Notifications              []CalendarNotification
}

type CalendarEventCardType int

const (
	CalendarEventCardClear CalendarEventCardType = iota
	CalendarEventCardEncrypted
	CalendarEventCardSigned
	CalendarEventCardEncryptedAndSigned
)

func (t CalendarEventCardType) Signed() bool {
	switch t {
	case CalendarEventCardSigned, CalendarEventCardEncryptedAndSigned:
		return true
	default:
		return false
	}
}

func (t CalendarEventCardType) Encrypted() bool {
	switch t {
	case CalendarEventCardEncrypted, CalendarEventCardEncryptedAndSigned:
		return true
	default:
		return false
	}
}

type CalendarEventCard struct {
	Type      CalendarEventCardType
	Data      string
	Signature string
	MemberID  string `json:",omitempty"`
	Author    string `json:",omitempty"`
}

type CalendarNotificationType int

const (
	CalendarNotificationEmail CalendarNotificationType = iota
	CalendarNotificationDevice
)

func (t CalendarNotificationType) ToIcalAction() string {
	switch t {
	case CalendarNotificationEmail:
		return "EMAIL"
	case CalendarNotificationDevice:
		return "DISPLAY"
	default:
		return ""
	}
}

func ValarmActionToCalendarNotificationType(action string) CalendarNotificationType {
	switch action {
	case "EMAIL":
		return CalendarNotificationEmail
	case "DISPLAY":
		fallthrough
	default:
		return CalendarNotificationDevice
	}
}

type CalendarNotification struct {
	Type    CalendarNotificationType
	Trigger string
}

// FindMemberViewFromKeyring returns the calendar membership belonging to one
// of the addresses in the keyring.
func FindMemberViewFromKeyring(members []CalendarMemberView, kr openpgp.KeyRing) (*CalendarMemberView, error) {
	for i := range members {
		for _, userKey := range kr.DecryptionKeys() {
			for _, identity := range userKey.Entity.Identities {
				if identity.UserId != nil && members[i].Email == identity.UserId.Email {
					return &members[i], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("FindMemberViewFromKeyring: could not find a CalendarMemberView for the provided keyring")
}

// DecryptKeyring unlocks the calendar's private keys with the member
// passphrase encrypted to the user's keyring.
func (bootstrap *CalendarBootstrap) DecryptKeyring(userKr openpgp.KeyRing) (openpgp.KeyRing, error) {
	member, err := FindMemberViewFromKeyring(bootstrap.Members, userKr)
	if err != nil {
		return nil, fmt.Errorf("DecryptKeyring: failed to find member view: (%w)", err)
	}

	var calKr openpgp.EntityList
	for _, key := range bootstrap.Keys {
		var passphrase *CalendarMemberPassphrase
		for i := range bootstrap.Passphrase.MemberPassphrases {
			if bootstrap.Passphrase.MemberPassphrases[i].MemberID == member.ID {
				passphrase = &bootstrap.Passphrase.MemberPassphrases[i]
				break
			}
		}
		if passphrase == nil {
			return nil, fmt.Errorf("DecryptKeyring: could not find MemberPassphrase for MemberID: %s", member.ID)
		}

		passphraseEnc, err := armor.Decode(strings.NewReader(passphrase.Passphrase))
		if err != nil {
			return nil, fmt.Errorf("DecryptKeyring: failed to decode passphrase: (%w)", err)
		}

		md, err := openpgp.ReadMessage(passphraseEnc.Body, userKr, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("DecryptKeyring: failed to read message: (%w)", err)
		}

		passphraseBytes, err := io.ReadAll(md.UnverifiedBody)
		if err != nil {
			return nil, fmt.Errorf("DecryptKeyring: failed to read passphrase body: (%w)", err)
		}

		keyKr, err := openpgp.ReadArmoredKeyRing(strings.NewReader(key.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("DecryptKeyring: failed to read armored key ring: (%w)", err)
		}

		for _, calKey := range keyKr {
			err = calKey.PrivateKey.Decrypt(passphraseBytes)
			if err != nil {
				return nil, fmt.Errorf("DecryptKeyring: failed to decrypt private key: (%w)", err)
			}

			for _, subKey := range calKey.Subkeys {
				err := subKey.PrivateKey.Decrypt(passphraseBytes)
				if err != nil {
					return nil, fmt.Errorf("DecryptKeyring: failed to decrypt subkey: (%w)", err)
				}
			}
		}

		calKr = append(calKr, keyKr...)
	}
	return calKr, nil
}

func (card *CalendarEventCard) Read(userKr openpgp.KeyRing, calKr openpgp.KeyRing, keyPacket string) (*openpgp.MessageDetails, error) {
	if !card.Type.Encrypted() {
		md := &openpgp.MessageDetails{
			IsEncrypted:    false,
			IsSigned:       false,
			UnverifiedBody: strings.NewReader(card.Data),
		}

		if !card.Type.Signed() {
			return md, nil
		}

		signed := strings.NewReader(card.Data)
		signature := strings.NewReader(card.Signature)
		signer, err := openpgp.CheckArmoredDetachedSignature(userKr, signed, signature, nil)
		md.IsSigned = true
		md.SignatureError = err
		if signer != nil {
			md.SignedByKeyId = signer.PrimaryKey.KeyId
			md.SignedBy = entityPrimaryKey(signer)
		}
		return md, nil
	}

	keyPacketData := base64.NewDecoder(base64.StdEncoding, strings.NewReader(keyPacket))
	ciphertext := base64.NewDecoder(base64.StdEncoding, strings.NewReader(card.Data))
	msg := io.MultiReader(keyPacketData, ciphertext)
	md, err := openpgp.ReadMessage(msg, calKr, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("Read: failed to read message: (%w)", err)
	}

	if card.Type.Signed() {
		r := &detachedSignatureReader{
			md:        md,
			signature: strings.NewReader(card.Signature),
			keyring:   userKr,
		}
		r.body = io.TeeReader(md.UnverifiedBody, &r.signed)

		md.UnverifiedBody = r
	}

	return md, nil
}

func (c *Client) ListCalendars() ([]*Calendar, error) {
	req, err := c.newRequest(http.MethodGet, calendarPath, nil)
	if err != nil {
		return nil, fmt.Errorf("ListCalendars: failed to create new request: (%w)", err)
	}

	var respData struct {
		resp
		Calendars []*Calendar
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, fmt.Errorf("ListCalendars: failed to execute JSON request: (%w)", err)
	}

	return respData.Calendars, nil
}

func (c *Client) BootstrapCalendar(id string) (*CalendarBootstrap, error) {
	req, err := c.newRequest(http.MethodGet, calendarPath+"/"+id+"/bootstrap", nil)
	if err != nil {
		return nil, fmt.Errorf("BootstrapCalendar: failed to create new request: (%w)", err)
	}

	var respData struct {
		resp
		*CalendarBootstrap
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, fmt.Errorf("BootstrapCalendar: failed to execute JSON request: (%w)", err)
	}

	return respData.CalendarBootstrap, nil
}

const (
	// eventPageSize is the largest page Proton serves.
	eventPageSize = 100
	// eventPageWorkers is how many pages are fetched at once. Proton answers
	// each page slowly, so fetching one after another took minutes for a few
	// thousand events.
	eventPageWorkers = 4
)

// ListCalendarEvents returns every event in a calendar.
//
// Proton paginates the listing and gives no total, so pages are fetched in
// rounds of eventPageWorkers until a round contains a short or empty page.
func (c *Client) ListCalendarEvents(calendarID string) ([]*CalendarEvent, error) {
	var all []*CalendarEvent
	for first := 0; ; first += eventPageWorkers {
		pages := make([][]*CalendarEvent, eventPageWorkers)
		errs := make([]error, eventPageWorkers)
		var wg sync.WaitGroup
		for i := range eventPageWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pages[i], errs[i] = c.listCalendarEventsPage(calendarID, first+i)
			}()
		}
		wg.Wait()

		for i := range eventPageWorkers {
			if errs[i] != nil {
				return nil, errs[i]
			}
			all = append(all, pages[i]...)
			if len(pages[i]) < eventPageSize {
				return all, nil
			}
		}
	}
}

func (c *Client) listCalendarEventsPage(calendarID string, page int) ([]*CalendarEvent, error) {
	v := url.Values{}
	v.Set("Page", strconv.Itoa(page))
	v.Set("PageSize", strconv.Itoa(eventPageSize))

	req, err := c.newRequest(http.MethodGet, calendarPath+"/"+calendarID+"/events?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("ListCalendarEvents: calendar %s page %d: %w", calendarID, page, err)
	}

	var respData struct {
		resp
		Events []*CalendarEvent
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, fmt.Errorf("ListCalendarEvents: calendar %s page %d: %w", calendarID, page, err)
	}
	return respData.Events, nil
}

func (c *Client) GetCalendarEvent(calendarID string, eventID string) (*CalendarEvent, error) {
	req, err := c.newRequest(http.MethodGet, calendarPath+"/"+calendarID+"/events/"+eventID, nil)
	if err != nil {
		return nil, fmt.Errorf("GetCalendarEvent: failed to create new request for calendarID %s and eventID %s: (%w)", calendarID, eventID, err)
	}

	var respData struct {
		resp
		Event *CalendarEvent
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, fmt.Errorf("GetCalendarEvent: failed to execute JSON request for calendarID %s and eventID %s: (%w)", calendarID, eventID, err)
	}

	return respData.Event, nil
}

func concat(slices [][]string) []string {
	var totalLen int
	for _, s := range slices {
		totalLen += len(s)
	}
	tmp := make([]string, totalLen)
	var i int
	for _, s := range slices {
		i += copy(tmp[i:], s)
	}
	return tmp
}

type CalendarEventCreateOrUpdateData struct {
	Color                *string
	Notifications        []CalendarNotification
	CalendarKeyPacket    string              `json:",omitempty"`
	CalendarEventContent []CalendarEventCard `json:",omitempty"`
	SharedKeyPacket      string              `json:",omitempty"`
	SharedEventContent   []CalendarEventCard `json:",omitempty"`
	Permissions          int                 `json:",omitempty"`
	IsOrganizer          int                 `json:",omitempty"`
}

type CalendarEventSyncReq struct {
	MemberID string
	Events   []interface{}
}

type CalendarEventCreateSyncEntry struct {
	Overwrite int
	Event     *CalendarEventCreateOrUpdateData
}

type CalendarEventUpdateSyncEntry struct {
	ID    string
	Event *CalendarEventCreateOrUpdateData
}

var sharedSignedFields = []string{
	"uid",
	"dtstamp",
	"dtstart",
	"dtend",
	"recurrence-id",
	"rrule",
	"exdate",
	"organizer",
	"sequence",
}
var sharedEncryptedFields = []string{
	"uid",
	"dtstamp",
	"created",
	"description",
	"summary",
	"location",
}

var calendarSignedFields = []string{
	"uid",
	"dtstamp",
	"exdate",
	"status",
	"transp",
}
var calendarEncryptedFields = []string{
	"uid",
	"dtstamp",
	"comment",
}

var requiredSet = map[string]struct{}{
	"uid":     {},
	"dtstamp": {},
}

var usedFields = concat([][]string{
	sharedSignedFields,
	sharedEncryptedFields,
	calendarSignedFields,
	calendarEncryptedFields,
})

func pickProps(event *ical.Event, propNames []string) *ical.Component {
	evt := ical.NewEvent()
	for _, propName := range propNames {
		props := event.Props.Values(propName)
		if len(props) > 0 {
			evt.Props[props[0].Name] = props
		}
	}

	return evt.Component
}

func makeIcal(props ical.Props, components ...*ical.Component) *ical.Calendar {
	cal := ical.NewCalendar()

	if props != nil {
		maps.Copy(cal.Props, props)
	}

	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Proton AG//web-calendar 5.0.33.2//EN")

	if components != nil {
		cal.Children = append(cal.Children, components...)
	}

	return cal
}

func getEventParts(event *ical.Event) (map[CalendarEventCardType]*ical.Calendar, map[CalendarEventCardType]*ical.Calendar) {
	sharedPart := make(map[CalendarEventCardType]*ical.Calendar)
	sharedPart[CalendarEventCardSigned] = makeIcal(nil, pickProps(event, sharedSignedFields))
	sharedPart[CalendarEventCardEncryptedAndSigned] = makeIcal(nil, pickProps(event, sharedEncryptedFields))

	calendarPart := make(map[CalendarEventCardType]*ical.Calendar)
	calendarPart[CalendarEventCardSigned] = makeIcal(nil, pickProps(event, calendarSignedFields))
	calendarPart[CalendarEventCardEncryptedAndSigned] = makeIcal(nil, pickProps(event, calendarEncryptedFields))

	// Personal and attendee parts are not supported. Every property that is
	// not placed in a specific part goes into the encrypted shared part. Work
	// on a copy: the caller's event must come out of this unchanged.
	remaining := maps.Clone(event.Props)
	for _, propName := range usedFields {
		remaining.Del(strings.ToUpper(propName))
	}
	encryptedShared := sharedPart[CalendarEventCardEncryptedAndSigned].Events()[0].Component
	for name, props := range remaining {
		encryptedShared.Props[strings.ToUpper(name)] = props
	}

	return sharedPart, calendarPart
}

func encodePart(part map[CalendarEventCardType]*ical.Calendar) (map[CalendarEventCardType]string, error) {
	encodedPart := make(map[CalendarEventCardType]string)
	for cardType, card := range part {
		if card == nil {
			encodedPart[cardType] = ""
			continue
		}
		icalData := new(bytes.Buffer)
		icalEncoder := ical.NewEncoder(icalData)
		err := icalEncoder.Encode(card)
		if err != nil {
			return nil, fmt.Errorf("encodePart: failed to encode card to ical: (%w)", err)
		}

		encodedPart[cardType] = icalData.String()
	}

	return encodedPart, nil
}
func encodePartOptimized(part map[CalendarEventCardType]*ical.Calendar) (map[CalendarEventCardType]string, error) {
	for cardType, card := range part {
		evt := card.Children[0]
		if len(evt.Children) > 0 {
			continue
		}

		if len(evt.Props) > len(requiredSet) {
			continue
		}

		isExactMatch := true
		for propName := range evt.Props {
			if _, exists := requiredSet[strings.ToLower(propName)]; !exists {
				isExactMatch = false
				break
			}
		}

		if isExactMatch && len(evt.Props) == len(requiredSet) {
			part[cardType] = nil
		}
	}

	return encodePart(part)
}

func decryptSessionKey(sessionKey string, calKr openpgp.KeyRing) (*packet.EncryptedKey, error) {
	if sessionKey == "" {
		return nil, nil
	}

	sharedKeyPacket := base64.NewDecoder(base64.StdEncoding, strings.NewReader(sessionKey))
	packetReader := packet.NewReader(sharedKeyPacket)

	pkt, err := packetReader.Next()
	if err != nil {
		return nil, fmt.Errorf("decryptSessionKey: failed to read next packet: (%w)", err)
	}

	switch pkt := pkt.(type) {
	case *packet.EncryptedKey:
		keys := calKr.KeysById(pkt.KeyId)
		if len(keys) == 0 {
			return nil, fmt.Errorf("decryptSessionKey: no calendar key with KeyId %d", pkt.KeyId)
		}
		decryptionKey := keys[0]
		if decryptionKey.PrivateKey == nil || decryptionKey.PrivateKey.Encrypted {
			return nil, fmt.Errorf("decryptSessionKey: decryption private key must be decrypted for KeyId %d", pkt.KeyId)
		}

		err := pkt.Decrypt(decryptionKey.PrivateKey, nil)
		if err != nil {
			return nil, fmt.Errorf("decryptSessionKey: failed to decrypt encrypted key for KeyId %d: (%w)", pkt.KeyId, err)
		}
		return pkt, nil
	default:
		return nil, fmt.Errorf("decryptSessionKey: unexpected packet type, unable to decrypt session key")
	}
}

func getOrGenerateSessionKey(keyPacket string, calKr openpgp.KeyRing, config *packet.Config) (*packet.EncryptedKey, string, error) {
	var sessionKey *packet.EncryptedKey
	var encryptedSessionKey string
	if keyPacket != "" {
		encryptedSessionKey = keyPacket

		var err error
		sessionKey, err = decryptSessionKey(encryptedSessionKey, calKr)
		if err != nil {
			return nil, "", fmt.Errorf("getOrGenerateSessionKey: failed to decrypt session key: (%w)", err)
		}
	}

	if sessionKey == nil {
		var err error
		sessionKey, err = generateUnencryptedKey(packet.CipherAES256, config)
		if err != nil {
			return nil, "", fmt.Errorf("getOrGenerateSessionKey: failed to generate unencrypted key: (%w)", err)
		}

		calKeys, err := firstEntity(calKr)
		if err != nil {
			return nil, "", fmt.Errorf("getOrGenerateSessionKey: calendar keyring: %w", err)
		}
		calEncryptionKey, ok := encryptionKey(calKeys, config.Now())
		if !ok {
			return nil, "", fmt.Errorf("getOrGenerateSessionKey: could not find encryption key for calendar keys")
		}
		encryptedSessionKey, err = serializeEncryptedKey(sessionKey, calEncryptionKey.PublicKey, config)
		if err != nil {
			return nil, "", fmt.Errorf("getOrGenerateSessionKey: failed to serialize encrypted key: (%w)", err)
		}
	}

	return sessionKey, encryptedSessionKey, nil
}

// firstEntity returns the primary entity of a keyring: Proton lists the
// primary key first for both user and calendar keyrings.
func firstEntity(kr openpgp.KeyRing) (*openpgp.Entity, error) {
	entities, ok := kr.(openpgp.EntityList)
	if !ok || len(entities) == 0 {
		return nil, errors.New("keyring is empty")
	}
	return entities[0], nil
}

func encryptPart(part string, key *packet.EncryptedKey, signer *openpgp.Entity, config *packet.Config) (string, error) {
	var signKey *packet.PrivateKey
	if signer != nil {
		signKeys, ok := signingKey(signer, config.Now())
		if !ok {
			return "", fmt.Errorf("encryptPart: no valid signing keys available")
		}
		signKey = signKeys.PrivateKey
		if signKey == nil {
			return "", fmt.Errorf("encryptPart: no private key found in signing key")
		}
		if signKey.Encrypted {
			return "", fmt.Errorf("encryptPart: signing key must be decrypted")
		}
	}

	encryptedBuf := new(bytes.Buffer)
	encryptedTextWriter := base64.NewEncoder(base64.StdEncoding, encryptedBuf)

	hints := openpgp.FileHints{
		IsBinary: true,
		ModTime:  config.Now(),
	}

	clearTextWriter, err := symetricallyEncrypt(encryptedTextWriter, key, signKey, &hints, config)
	if err != nil {
		return "", fmt.Errorf("encryptPart: failed to encrypt text: (%w)", err)
	}

	_, err = clearTextWriter.Write([]byte(part))
	if err != nil {
		return "", fmt.Errorf("encryptPart: failed to write part data: (%w)", err)
	}

	err = clearTextWriter.Close()
	if err != nil {
		return "", fmt.Errorf("encryptPart: failed to close clear text writer: (%w)", err)
	}

	err = encryptedTextWriter.Close()
	if err != nil {
		return "", fmt.Errorf("encryptPart: failed to close encrypted text writer: (%w)", err)
	}

	return encryptedBuf.String(), nil
}

func signPart(part string, signer *openpgp.Entity, config *packet.Config) (string, error) {
	signatureBuf := new(bytes.Buffer)
	if err := openpgp.ArmoredDetachSignText(signatureBuf, signer, strings.NewReader(part), config); err != nil {
		return "", fmt.Errorf("signPart: failed to sign part: (%w)", err)
	}

	return signatureBuf.String(), nil
}

// makeUpdateData encrypts and signs an event for upload. oldEvent is the
// stored version being replaced, or nil for a new event.
func makeUpdateData(oldEvent *CalendarEvent, event ical.Event, calKr openpgp.KeyRing, userKr openpgp.KeyRing) (*CalendarEventCreateOrUpdateData, error) {
	isCreate := oldEvent == nil

	sharedPartCal, calendarPartCal := getEventParts(&event)
	sharedPart, err := encodePart(sharedPartCal)
	if err != nil {
		return nil, fmt.Errorf("makeUpdateData: failed to encode shared part: (%w)", err)
	}
	calendarPart, err := encodePartOptimized(calendarPartCal)
	if err != nil {
		return nil, fmt.Errorf("makeUpdateData: failed to encode calendar part: (%w)", err)
	}

	config := &packet.Config{}
	data := CalendarEventCreateOrUpdateData{}
	data.Permissions = 1

	color := event.Props.Get("color")
	if color != nil && color.Value != "" {
		data.Color = &color.Value
	}

	notifications := make([]CalendarNotification, 0)
	for _, child := range event.Children {
		if child.Name != ical.CompAlarm {
			continue
		}

		notification := CalendarNotification{}

		action := child.Props.Get("ACTION")
		notification.Type = ValarmActionToCalendarNotificationType(action.Value)

		trigger := child.Props.Get("TRIGGER")
		notification.Trigger = trigger.Value

		notifications = append(notifications, notification)
	}

	if len(notifications) > 0 {
		data.Notifications = notifications
	}

	if oldEvent != nil {
		data.IsOrganizer = oldEvent.IsOrganizer
	} else {
		data.IsOrganizer = 1
	}

	userKeys, err := firstEntity(userKr)
	if err != nil {
		return nil, fmt.Errorf("makeUpdateData: user keyring: %w", err)
	}
	if signedSharedPart, ok := sharedPart[CalendarEventCardSigned]; ok && signedSharedPart != "" {
		signature, err := signPart(signedSharedPart, userKeys, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to sign shared part: (%w)", err)
		}

		card := CalendarEventCard{
			Type:      CalendarEventCardSigned,
			Data:      signedSharedPart,
			Signature: signature,
		}

		data.SharedEventContent = append(data.SharedEventContent, card)
	}
	if encryptedSharedPart, ok := sharedPart[CalendarEventCardEncryptedAndSigned]; ok && encryptedSharedPart != "" {
		sharedKeyPacket := ""
		if oldEvent != nil {
			sharedKeyPacket = oldEvent.SharedKeyPacket
		}
		sharedSessionKey, encryptedSharedSessionKey, err := getOrGenerateSessionKey(sharedKeyPacket, calKr, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to get or generate session key for shared part: (%w)", err)
		}

		if isCreate || sharedKeyPacket == "" {
			data.SharedKeyPacket = encryptedSharedSessionKey
		}

		signature, err := signPart(encryptedSharedPart, userKeys, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to sign encrypted shared part: (%w)", err)
		}

		encryptedData, err := encryptPart(encryptedSharedPart, sharedSessionKey, nil, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to encrypt shared part: (%w)", err)
		}

		card := CalendarEventCard{
			Type:      CalendarEventCardEncryptedAndSigned,
			Data:      encryptedData,
			Signature: signature,
		}

		data.SharedEventContent = append(data.SharedEventContent, card)
	}

	if signedCalendarPart, ok := calendarPart[CalendarEventCardSigned]; ok && signedCalendarPart != "" {
		signature, err := signPart(signedCalendarPart, userKeys, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to sign calendar part: (%w)", err)
		}

		card := CalendarEventCard{
			Type:      CalendarEventCardSigned,
			Data:      signedCalendarPart,
			Signature: signature,
		}

		data.CalendarEventContent = append(data.CalendarEventContent, card)
	}
	if encryptedCalendarPart, ok := calendarPart[CalendarEventCardEncryptedAndSigned]; ok && encryptedCalendarPart != "" {
		calendarKeyPacket := ""
		if oldEvent != nil {
			calendarKeyPacket = oldEvent.CalendarKeyPacket
		}
		calendarSessionKey, encryptedCalendarSessionKey, err := getOrGenerateSessionKey(calendarKeyPacket, calKr, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to get or generate session key for calendar part: (%w)", err)
		}

		if isCreate || calendarKeyPacket == "" {
			data.CalendarKeyPacket = encryptedCalendarSessionKey
		}

		signature, err := signPart(encryptedCalendarPart, userKeys, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to sign encrypted calendar part: (%w)", err)
		}

		encryptedData, err := encryptPart(encryptedCalendarPart, calendarSessionKey, nil, config)
		if err != nil {
			return nil, fmt.Errorf("makeUpdateData: failed to encrypt calendar part: (%w)", err)
		}

		card := CalendarEventCard{
			Type:      CalendarEventCardEncryptedAndSigned,
			Data:      encryptedData,
			Signature: signature,
		}

		data.CalendarEventContent = append(data.CalendarEventContent, card)
	}

	return &data, nil
}

// EventWrite is one entry of a batch write: create (empty ID),
// update (existing ID) or delete (nil Event).
type EventWrite struct {
	ID    string
	Event *ical.Event
}

type CalendarEventDeleteSyncEntry struct {
	ID             string
	DeletionReason int
}

// SyncCalendarEvents applies several writes to a calendar in one request,
// as Proton's own client does. The result has one entry per write, in the
// same order; deletes yield nil. Any rejected entry fails the whole call.
func (c *Client) SyncCalendarEvents(calID string, writes []EventWrite, userKr openpgp.KeyRing) ([]*CalendarEvent, error) {
	if len(writes) == 0 {
		return nil, nil
	}
	start := time.Now()

	entries := make([]interface{}, len(writes))
	var memberID string

	needKeys := false
	for _, w := range writes {
		if w.Event != nil {
			needKeys = true
		}
	}
	if needKeys {
		bootstrap, err := c.BootstrapCalendar(calID)
		if err != nil {
			return nil, fmt.Errorf("SyncCalendarEvents: bootstrapping calendar %s: %w", calID, err)
		}
		calKr, err := bootstrap.DecryptKeyring(userKr)
		if err != nil {
			return nil, fmt.Errorf("SyncCalendarEvents: decrypting keys of calendar %s: %w", calID, err)
		}
		member, err := FindMemberViewFromKeyring(bootstrap.Members, userKr)
		if err != nil {
			return nil, fmt.Errorf("SyncCalendarEvents: %w", err)
		}
		memberID = member.ID

		// Each update needs the stored event first; fetch them in parallel.
		errs := make([]error, len(writes))
		sem := make(chan struct{}, eventPageWorkers)
		var wg sync.WaitGroup
		for i, w := range writes {
			if w.Event == nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				entries[i], errs[i] = c.prepareWrite(calID, w, calKr, userKr)
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return nil, err
			}
		}
	}
	for i, w := range writes {
		if w.Event == nil {
			entries[i] = CalendarEventDeleteSyncEntry{ID: w.ID}
		}
	}

	prepared := time.Now()
	body := CalendarEventSyncReq{MemberID: memberID, Events: entries}
	req, err := c.newJSONRequest(http.MethodPut, calendarPath+"/"+calID+"/events/sync", body)
	if err != nil {
		return nil, fmt.Errorf("SyncCalendarEvents: %w", err)
	}
	var respData struct {
		resp
		Responses []struct {
			Index    int
			Response struct {
				resp
				Event *CalendarEvent
			}
		}
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, fmt.Errorf("SyncCalendarEvents: %w", err)
	}
	log.Printf("protonmail: synced %d events to calendar %s: prepared in %s, stored in %s", len(writes), calID,
		prepared.Sub(start).Round(time.Millisecond), time.Since(prepared).Round(time.Millisecond))

	results := make([]*CalendarEvent, len(writes))
	answered := 0
	for _, r := range respData.Responses {
		if r.Index < 0 || r.Index >= len(writes) {
			return nil, fmt.Errorf("SyncCalendarEvents: response for unknown entry %d", r.Index)
		}
		if err := r.Response.Err(); err != nil {
			return nil, fmt.Errorf("SyncCalendarEvents: Proton rejected entry %d: %w", r.Index, err)
		}
		if writes[r.Index].Event != nil && r.Response.Event == nil {
			return nil, fmt.Errorf("SyncCalendarEvents: no event in response for entry %d (code %d)", r.Index, r.Response.Code)
		}
		results[r.Index] = r.Response.Event
		answered++
	}
	if answered != len(writes) {
		return nil, fmt.Errorf("SyncCalendarEvents: %d writes, %d responses", len(writes), answered)
	}
	return results, nil
}

// prepareWrite encrypts one event for the batch, as a create or an update
// depending on whether Proton already has it.
func (c *Client) prepareWrite(calID string, w EventWrite, calKr, userKr openpgp.KeyRing) (interface{}, error) {
	var oldEvent *CalendarEvent
	if w.ID != "" {
		old, err := c.GetCalendarEvent(calID, w.ID)
		var apiErr *APIError
		switch {
		case errors.As(err, &apiErr) && apiErr.Code == CodeNotFound:
			// A client-chosen id for a new event; Proton assigns its own.
		case err != nil:
			return nil, fmt.Errorf("SyncCalendarEvents: fetching event %s: %w", w.ID, err)
		default:
			oldEvent = old
		}
	}

	data, err := makeUpdateData(oldEvent, *w.Event, calKr, userKr)
	if err != nil {
		return nil, fmt.Errorf("SyncCalendarEvents: preparing event %s: %w", w.ID, err)
	}
	if oldEvent == nil {
		return CalendarEventCreateSyncEntry{Event: data}, nil
	}
	return CalendarEventUpdateSyncEntry{ID: w.ID, Event: data}, nil
}

// DeleteCalendarEvent deletes one event.
func (c *Client) DeleteCalendarEvent(calID string, eventID string) error {
	_, err := c.SyncCalendarEvents(calID, []EventWrite{{ID: eventID}}, nil)
	return err
}
