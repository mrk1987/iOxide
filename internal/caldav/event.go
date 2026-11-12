package caldav

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/mrk1987/ioxide/internal/protonmail"
)

// calendarObject decrypts a Proton event into a CalDAV calendar object.
func (b *backend) calendarObject(calID string, calKeys openpgp.KeyRing, event *protonmail.CalendarEvent, settings protonmail.CalendarSettings) (*caldav.CalendarObject, error) {
	authorKeys, err := b.keysForAuthor(event.Author)
	if err != nil {
		return nil, err
	}

	if event.Notifications == nil {
		if event.FullDay == 0 {
			event.Notifications = settings.DefaultPartDayNotifications
		} else {
			event.Notifications = settings.DefaultFullDayNotifications
		}
	}

	cal, err := toICalendar(event, authorKeys, calKeys)
	if err != nil {
		return nil, fmt.Errorf("caldav: event %s/%s: %w", calID, event.ID, err)
	}

	return &caldav.CalendarObject{
		Path:    objectPath(calID, event.ID),
		ModTime: event.LastEditTime.Time(),
		ETag:    etag(event),
		Data:    cal,
	}, nil
}

func etag(event *protonmail.CalendarEvent) string {
	return fmt.Sprintf("%X%s", int64(event.LastEditTime), event.ID)
}

// keysForAuthor returns the keyring used to verify an event's signatures.
//
// Events authored by the account owner, and events with no author at all
// (holiday calendars, imports, shared calendars), are verified against the
// account's own keys. Asking Proton for the public keys of an empty address
// is rejected with "The Email is required".
func (b *backend) keysForAuthor(author string) (openpgp.EntityList, error) {
	if author == "" || b.ownEmails[strings.ToLower(author)] {
		return b.privateKeys, nil
	}

	b.keysMu.Lock()
	keys, cached := b.authorKeys[author]
	b.keysMu.Unlock()
	if cached {
		return keys, nil
	}

	resp, err := b.client.GetPublicKeys(author)
	if err != nil {
		return nil, fmt.Errorf("caldav: fetching public keys of %s: %w", author, err)
	}
	for _, key := range resp.Keys {
		entity, err := key.Entity()
		if err != nil {
			return nil, fmt.Errorf("caldav: parsing public key of %s: %w", author, err)
		}
		keys = append(keys, entity)
	}

	b.keysMu.Lock()
	b.authorKeys[author] = keys
	b.keysMu.Unlock()
	return keys, nil
}

// toICalendar merges the event's encrypted and signed cards into one VCALENDAR
// holding a single VEVENT.
func toICalendar(event *protonmail.CalendarEvent, authorKeys, calKeys openpgp.KeyRing) (*ical.Calendar, error) {
	merged := ical.NewEvent()
	calProps := ical.Props{}

	readCards := func(cards []protonmail.CalendarEventCard, keyPacket string) error {
		for _, card := range cards {
			props, err := readEventCard(merged, card, authorKeys, calKeys, keyPacket)
			if err != nil {
				return err
			}
			maps.Copy(calProps, props)
		}
		return nil
	}
	if err := readCards(event.SharedEvents, event.SharedKeyPacket); err != nil {
		return nil, fmt.Errorf("shared card: %w", err)
	}
	if err := readCards(event.CalendarEvents, event.CalendarKeyPacket); err != nil {
		return nil, fmt.Errorf("calendar card: %w", err)
	}

	for _, notification := range event.Notifications {
		alarm := ical.NewComponent(ical.CompAlarm)
		trigger := ical.NewProp("TRIGGER")
		trigger.SetValueType(ical.ValueDuration)
		trigger.Value = notification.Trigger
		alarm.Props.SetText("ACTION", notification.Type.ToIcalAction())
		alarm.Props.Add(trigger)
		merged.Children = append(merged.Children, alarm)
	}

	cal := ical.NewCalendar()
	maps.Copy(cal.Props, calProps)
	cal.Children = append(cal.Children, merged.Component)
	return cal, nil
}

// readEventCard decrypts one card, copies its VEVENT properties onto event and
// returns the VCALENDAR-level properties it carried.
func readEventCard(event *ical.Event, card protonmail.CalendarEventCard, authorKeys, calKeys openpgp.KeyRing, keyPacket string) (ical.Props, error) {
	md, err := card.Read(authorKeys, calKeys, keyPacket)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	data, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}
	if md.SignatureError != nil {
		// Old and imported events are often signed with a key that no longer
		// verifies. For a personal bridge, being able to read the event
		// matters more than the signature, so serve it and say so.
		log.Printf("caldav: serving event with unverified signature: %v", md.SignatureError)
	}

	cal, err := decodeICS(data)
	if err != nil {
		return nil, err
	}

	events := cal.Events()
	if len(events) != 1 {
		return nil, errors.New("expected exactly one VEVENT in card")
	}
	for _, props := range events[0].Props {
		for i := range props {
			event.Props.Set(&props[i])
		}
	}
	return cal.Props, nil
}

// decodeICS parses iCalendar text, repairing it first if the strict parser
// rejects it.
func decodeICS(data []byte) (*ical.Calendar, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err == nil {
		return cal, nil
	}

	// Imported events sometimes carry an unescaped newline inside a value.
	// The second physical line then has no name:value separator, which the
	// parser reports as "missing colon". Repairing and retrying is far better
	// than dropping the event: one calendar lost about 2000 events this way.
	repaired, rerr := ical.NewDecoder(bytes.NewReader(sanitizeICS(data))).Decode()
	if rerr != nil {
		return nil, fmt.Errorf("parsing: %w", err)
	}
	log.Printf("caldav: recovered a malformed event")
	return repaired, nil
}

// sanitizeICS repairs iCalendar text with unescaped line breaks inside
// property values. A physical line that is neither a fold continuation nor a
// name:value content line is merged back onto the previous line; blank lines
// are dropped. Lines that already look valid pass through untouched.
func sanitizeICS(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var out []string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case line == "":
			continue
		case line[0] == ' ' || line[0] == '\t':
			out = append(out, line) // fold continuation
		case !strings.ContainsAny(line, ":;"):
			if len(out) > 0 {
				out[len(out)-1] += line // orphaned value continuation
			}
		default:
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, "\r\n") + "\r\n")
}
