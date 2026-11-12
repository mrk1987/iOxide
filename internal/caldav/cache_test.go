package caldav

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

func obj(t *testing.T, vevent string) caldav.CalendarObject {
	t.Helper()
	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:1\r\nDTSTAMP:20260101T000000Z\r\n" +
		vevent + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return caldav.CalendarObject{Data: cal}
}

func TestOverlaps(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name, ev string
		want     bool
	}{
		{"inside", "DTSTART:20261104T100000Z\r\nDTEND:20261104T110000Z", true},
		{"before", "DTSTART:20221104T100000Z\r\nDTEND:20221104T110000Z", false},
		{"after", "DTSTART:20270104T100000Z\r\nDTEND:20270104T110000Z", false},
		{"spans start", "DTSTART:20261031T100000Z\r\nDTEND:20261101T100000Z", true},
		{"recurring old", "DTSTART:20200104T100000Z\r\nRRULE:FREQ=WEEKLY", true},
		{"all day inside", "DTSTART;VALUE=DATE:20261105\r\nDTEND;VALUE=DATE:20261106", true},
	}
	for _, c := range cases {
		if got := overlaps(obj(t, c.ev), start, end); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if !overlaps(obj(t, "DTSTART:20221104T100000Z"), time.Time{}, time.Time{}) {
		t.Error("zero range must match everything")
	}
}
