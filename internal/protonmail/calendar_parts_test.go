package protonmail

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func TestGetEventPartsLeavesTheEventUntouched(t *testing.T) {
	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:series-1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20261112T200000Z\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\nX-CUSTOM:kept\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	event := cal.Events()[0]

	shared, calendar := getEventParts(&event)

	for _, name := range []string{ical.PropUID, ical.PropDateTimeStamp, ical.PropDateTimeStart, ical.PropRecurrenceRule, ical.PropSummary, "X-CUSTOM"} {
		if event.Props.Get(name) == nil {
			t.Errorf("getEventParts removed %s from the caller's event", name)
		}
	}

	signed := shared[CalendarEventCardSigned].Events()[0]
	if signed.Props.Get(ical.PropRecurrenceRule) == nil || signed.Props.Get(ical.PropUID) == nil {
		t.Error("shared signed part lacks RRULE or UID")
	}
	encrypted := shared[CalendarEventCardEncryptedAndSigned].Events()[0]
	if encrypted.Props.Get(ical.PropSummary) == nil || encrypted.Props.Get("X-CUSTOM") == nil {
		t.Error("shared encrypted part lacks SUMMARY or the unknown property")
	}
	if encrypted.Props.Get(ical.PropRecurrenceRule) != nil {
		t.Error("RRULE leaked into the encrypted part")
	}
	if calendar[CalendarEventCardSigned].Events()[0].Props.Get(ical.PropUID) == nil {
		t.Error("calendar signed part lacks UID")
	}
}
