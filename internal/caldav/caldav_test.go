package caldav

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

func TestSplitCalendarPath(t *testing.T) {
	cases := []struct {
		path   string
		wantID string
		wantOK bool
	}{
		{"/caldav/calendars/abc", "abc", true},
		{"/caldav/calendars/abc/", "abc", true},
		{"/caldav/calendars/", "", false},
		{"/caldav/calendars/abc/def.ics", "", false},
		{"/caldav/", "", false},
		{"/other/abc", "", false},
	}
	for _, c := range cases {
		id, ok := splitCalendarPath(c.path)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("splitCalendarPath(%q) = %q, %v; want %q, %v", c.path, id, ok, c.wantID, c.wantOK)
		}
	}
}

func TestSplitObjectPath(t *testing.T) {
	cases := []struct {
		path          string
		wantCal, want string
		wantOK        bool
	}{
		{"/caldav/calendars/abc/def.ics", "abc", "def", true},
		{"/caldav/calendars/abc/def", "abc", "def", true},
		{"/caldav/calendars/abc/def.ics/", "abc", "def", true},
		{"/caldav/calendars/abc", "", "", false},
		{"/caldav/calendars/abc/", "", "", false},
		{"/caldav/calendars/abc/def/ghi.ics", "", "", false},
		{"/caldav/calendars//def.ics", "", "", false},
		{"/elsewhere/abc/def.ics", "", "", false},
	}
	for _, c := range cases {
		cal, ev, ok := splitObjectPath(c.path)
		if cal != c.wantCal || ev != c.want || ok != c.wantOK {
			t.Errorf("splitObjectPath(%q) = %q, %q, %v; want %q, %q, %v", c.path, cal, ev, ok, c.wantCal, c.want, c.wantOK)
		}
	}
}

func TestObjectPathRoundTrip(t *testing.T) {
	p := objectPath("cal1", "ev1")
	if p != "/caldav/calendars/cal1/ev1.ics" {
		t.Fatalf("objectPath = %q", p)
	}
	cal, ev, ok := splitObjectPath(p)
	if !ok || cal != "cal1" || ev != "ev1" {
		t.Fatalf("splitObjectPath(objectPath(...)) = %q, %q, %v", cal, ev, ok)
	}
}

func TestSanitizeICS(t *testing.T) {
	broken := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20260102T100000Z\r\n" +
		"DESCRIPTION:first line\nsecond line without separator\r\n" +
		"\r\n" +
		"SUMMARY:ok\r\n folded continuation\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	if _, err := ical.NewDecoder(strings.NewReader(broken)).Decode(); err == nil {
		t.Fatal("expected the strict parser to reject the broken input")
	}

	cal, err := decodeICS([]byte(broken))
	if err != nil {
		t.Fatalf("decodeICS: %v", err)
	}
	events := cal.Events()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	desc, _ := events[0].Props.Text(ical.PropDescription)
	if desc != "first linesecond line without separator" {
		t.Errorf("description = %q", desc)
	}
	summary, _ := events[0].Props.Text(ical.PropSummary)
	if summary != "okfolded continuation" {
		t.Errorf("summary = %q", summary)
	}
}

func TestSanitizeICSKeepsValidInput(t *testing.T) {
	valid := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:1\r\nSUMMARY:a;b:c\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if got := string(sanitizeICS([]byte(valid))); got != valid {
		t.Errorf("sanitizeICS changed valid input:\n%s", got)
	}
}

func TestPropPatchIsAccepted(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("PROPPATCH reached the wrapped handler")
	})
	h := withPropPatch(next)

	body := `<?xml version="1.0" encoding="UTF-8"?>
<A:propertyupdate xmlns:A="DAV:">
  <A:set>
    <A:prop>
      <B:calendar-color xmlns:B="http://apple.com/ns/ical/">#FF0000FF</B:calendar-color>
      <A:displayname>Work</A:displayname>
    </A:prop>
  </A:set>
  <A:remove>
    <A:prop><B:calendar-order xmlns:B="http://apple.com/ns/ical/"/></A:prop>
  </A:remove>
</A:propertyupdate>`
	req := httptest.NewRequest("PROPPATCH", "/caldav/calendars/abc/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body:\n%s", rec.Code, rec.Body)
	}

	var ms struct {
		XMLName  xml.Name `xml:"DAV: multistatus"`
		Response struct {
			Href     string `xml:"href"`
			PropStat struct {
				Prop struct {
					Names []struct{ XMLName xml.Name } `xml:",any"`
				} `xml:"prop"`
				Status string `xml:"status"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &ms); err != nil {
		t.Fatalf("response is not valid multistatus XML: %v\n%s", err, rec.Body)
	}
	if ms.Response.Href != "/caldav/calendars/abc/" {
		t.Errorf("href = %q", ms.Response.Href)
	}
	if !strings.Contains(ms.Response.PropStat.Status, "200") {
		t.Errorf("status = %q", ms.Response.PropStat.Status)
	}
	var got []string
	for _, n := range ms.Response.PropStat.Prop.Names {
		got = append(got, n.XMLName.Space+" "+n.XMLName.Local)
	}
	want := []string{"http://apple.com/ns/ical/ calendar-color", "DAV: displayname", "http://apple.com/ns/ical/ calendar-order"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("echoed props = %v, want %v", got, want)
	}
}

func TestPropPatchRejectsGarbage(t *testing.T) {
	h := withPropPatch(http.NotFoundHandler())
	req := httptest.NewRequest("PROPPATCH", "/caldav/calendars/abc/", bytes.NewReader([]byte("not xml")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestOtherMethodsPassThrough(t *testing.T) {
	called := false
	h := withPropPatch(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PROPFIND", "/caldav/", nil))
	if !called {
		t.Error("PROPFIND did not reach the wrapped handler")
	}
}
