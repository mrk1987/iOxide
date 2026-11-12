package caldav

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func parseEvent(t *testing.T, body string) *ical.Event {
	t.Helper()
	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\n" + body + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	ev := cal.Events()[0]
	return &ev
}

func TestSameEvent(t *testing.T) {
	// As the proxy renders a Proton event: a bare reminder with a VALUE parameter.
	base := "UID:1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=Europe/Oslo:20261112T203000\r\nDTEND;TZID=Europe/Oslo:20261112T213000\r\nSUMMARY:Dinner\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER;VALUE=DURATION:-PT15M\r\nEND:VALARM"
	// As iOS sends it back: bookkeeping, extensions and a decorated reminder.
	fromPhone := "UID:1\r\nDTSTAMP:20261112T185000Z\r\nSEQUENCE:3\r\nLAST-MODIFIED:20261112T185000Z\r\nCREATED:20260101T000000Z\r\n" +
		"DTSTART;TZID=Europe/Oslo:20261112T203000\r\nDTEND;TZID=Europe/Oslo:20261112T213000\r\nSUMMARY:Dinner\r\nX-APPLE-TRAVEL-ADVISORY-BEHAVIOR:AUTOMATIC\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nUID:alarm-1\r\nX-WR-ALARMUID:alarm-1\r\nDESCRIPTION:Reminder\r\nEND:VALARM"
	cases := []struct {
		name  string
		other string
		same  bool
	}{
		{"identical", base, true},
		{"as sent back by the phone", fromPhone, true},
		{"property order", "SUMMARY:Dinner\r\nDTEND;TZID=Europe/Oslo:20261112T213000\r\nDTSTART;TZID=Europe/Oslo:20261112T203000\r\nDTSTAMP:20260101T000000Z\r\nUID:1\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nEND:VALARM", true},
		{"moved", strings.Replace(base, "T203000", "T210000", 1), false},
		{"renamed", strings.Replace(base, "Dinner", "Supper", 1), false},
		{"alarm changed", strings.Replace(base, "-PT15M", "-PT30M", 1), false},
		{"alarm removed", "UID:1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;TZID=Europe/Oslo:20261112T203000\r\nDTEND;TZID=Europe/Oslo:20261112T213000\r\nSUMMARY:Dinner", false},
		{"second alarm added", base + "\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT1H\r\nEND:VALARM", false},
		{"timezone changed", strings.Replace(base, "DTSTART;TZID=Europe/Oslo", "DTSTART;TZID=Europe/London", 1), false},
	}
	a := parseEvent(t, base)
	for _, c := range cases {
		if got := sameEvent(a, parseEvent(t, c.other)); got != c.same {
			t.Errorf("%s: sameEvent = %v, want %v (differs in %v)", c.name, got, c.same, changedProps(a, parseEvent(t, c.other)))
		}
	}
	if sameEvent(a, nil) || sameEvent(nil, a) {
		t.Error("nil must never compare equal")
	}
}

func TestChangedPropsNamesOnly(t *testing.T) {
	a := parseEvent(t, "UID:1\r\nDTSTART:20261112T200000Z\r\nSUMMARY:Secret title\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nEND:VALARM")
	b := parseEvent(t, "UID:1\r\nDTSTART:20261112T210000Z\r\nSUMMARY:Secret title\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT30M\r\nEND:VALARM")
	got := strings.Join(changedProps(a, b), ",")
	if got != "DTSTART,VALARM" {
		t.Errorf("changedProps = %q, want DTSTART,VALARM", got)
	}
	if strings.Contains(got, "Secret") {
		t.Error("changedProps leaked a value")
	}
}
