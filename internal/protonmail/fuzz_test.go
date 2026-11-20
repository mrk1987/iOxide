package protonmail

import (
	"bytes"
	"testing"

	"github.com/emersion/go-ical"
)

// Events written by calendar clients are split into Proton's signed and
// encrypted parts before upload. Any event the parser accepts must split and
// encode without crashing the bridge.
func FuzzGetEventParts(f *testing.F) {
	f.Add("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:a-1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20261121T100000Z\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\nLOCATION:Room\r\nX-CUSTOM:kept\r\n" +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	f.Add("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	f.Fuzz(func(t *testing.T, src string) {
		cal, err := ical.NewDecoder(bytes.NewReader([]byte(src))).Decode()
		if err != nil {
			return
		}
		for _, ev := range cal.Events() {
			shared, calendar := getEventParts(&ev)
			if _, err := encodePart(shared); err != nil {
				continue
			}
			_, _ = encodePartOptimized(calendar)
		}
	})
}
