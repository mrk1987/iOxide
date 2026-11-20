package caldav

import (
	"strings"
	"testing"
)

const fuzzEventICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:a-1\r\n" +
	"DTSTAMP:20260101T000000Z\r\nDTSTART:20261121T100000Z\r\nDTEND:20261121T110000Z\r\n" +
	"SUMMARY:Lunch\r\nDESCRIPTION:line one\r\n line two\r\nRRULE:FREQ=WEEKLY\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

// Request paths come straight from calendar clients.
func FuzzSplitObjectPath(f *testing.F) {
	for _, seed := range []string{
		objectPath("cal", "event"),
		objectPath("cal", "event.ics"),
		homeSetPath + "cal/",
		homeSetPath + "cal/sub/event.ics",
		homeSetPath + "/event.ics",
		"/other/cal/event.ics",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, p string) {
		calID, eventID, ok := splitObjectPath(p)
		if !ok {
			return
		}
		if calID == "" || eventID == "" || strings.Contains(calID, "/") || strings.Contains(eventID, "/") {
			t.Fatalf("splitObjectPath(%q) = %q, %q: ids must be non-empty and contain no slash", p, calID, eventID)
		}
		c2, e2, ok2 := splitObjectPath(objectPath(calID, eventID))
		if !ok2 || c2 != calID || e2 != eventID {
			t.Fatalf("round trip of %q, %q gave %q, %q, %v", calID, eventID, c2, e2, ok2)
		}
	})
}

// iCalendar text comes from calendar clients and from events stored in
// Proton by other apps; neither may crash the bridge.
func FuzzDecodeICS(f *testing.F) {
	f.Add([]byte(fuzzEventICS))
	f.Add([]byte(strings.Replace(fuzzEventICS, "DESCRIPTION:line one\r\n line two", "DESCRIPTION:line one\r\nline two", 1)))
	f.Add([]byte("BEGIN:VCALENDAR\r\n\r\n\r\nEND:VCALENDAR"))
	f.Add([]byte("no colon\n\tfold\r:;\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		once := sanitizeICS(data)
		if twice := sanitizeICS(once); string(twice) != string(once) {
			t.Fatalf("sanitizeICS is not stable:\n%q\n%q", once, twice)
		}
		cal, err := decodeICS(data)
		if err != nil {
			return
		}
		for _, ev := range cal.Events() {
			_ = recurrenceID(&ev)
			_ = eventUID(&ev)
		}
	})
}
