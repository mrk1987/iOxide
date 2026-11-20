package protonmail

import (
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/emersion/go-ical"
)

func newEntity(t *testing.T, email string) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity(email, "", email, &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// Issue #8: on an account with several addresses, events were signed with
// the first address's key, and Proton rejected them with [2001] "Provide
// data signed using the address key". The key must belong to the address
// that owns the calendar.
func TestAddressSignerPicksTheCalendarAddress(t *testing.T) {
	alias := newEntity(t, "alias@example.com")
	owner := newEntity(t, "owner@example.com")
	kr := openpgp.EntityList{alias, owner}

	got, err := addressSigner(kr, "Owner@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != owner {
		t.Errorf("signer is %v, want the calendar address's key", got.PrimaryIdentity().UserId.Email)
	}

	if _, err := addressSigner(kr, "nobody@example.com"); err == nil {
		t.Error("no key for the calendar address must be an error, not a signature Proton rejects")
	}
}

func TestMakeUpdateDataSignsEveryPartWithTheGivenKey(t *testing.T) {
	alias := newEntity(t, "alias@example.com")
	owner := newEntity(t, "owner@example.com")
	calKr := openpgp.EntityList{newEntity(t, "calendar@example.com")}

	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:new-1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20261121T100000Z\r\nDTEND:20261121T110000Z\r\nSUMMARY:Created over CalDAV\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}

	data, err := makeUpdateData(nil, cal.Events()[0], calKr, owner)
	if err != nil {
		t.Fatal(err)
	}
	cards := append(append([]CalendarEventCard{}, data.SharedEventContent...), data.CalendarEventContent...)
	if len(cards) == 0 {
		t.Fatal("no signed cards")
	}
	for _, card := range cards {
		if card.Signature == "" {
			continue
		}
		// Signatures cover the plaintext; only the plain signed cards carry it.
		if card.Type != CalendarEventCardSigned {
			continue
		}
		if _, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{owner}, strings.NewReader(card.Data), strings.NewReader(card.Signature), nil); err != nil {
			t.Errorf("card %d is not signed by the calendar address key: %v", card.Type, err)
		}
		if _, err := openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{alias}, strings.NewReader(card.Data), strings.NewReader(card.Signature), nil); err == nil {
			t.Errorf("card %d verifies against another address's key", card.Type)
		}
	}
}

func TestPrimaryKeyComesFirst(t *testing.T) {
	keys := []*PrivateKey{{ID: "old"}, {ID: "primary", Primary: 1}, {ID: "older"}}
	got := primaryFirst(keys)
	ids := []string{}
	for _, k := range got {
		ids = append(ids, k.ID)
	}
	if strings.Join(ids, ",") != "primary,old,older" {
		t.Errorf("order = %v, want primary first, the rest unchanged", ids)
	}
	if keys[0].ID != "old" {
		t.Error("primaryFirst changed the caller's slice")
	}
}
