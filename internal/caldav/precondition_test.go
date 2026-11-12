package caldav

import (
	"strings"
	"testing"

	"github.com/emersion/go-webdav/caldav"
)

func TestCheckPreconditions(t *testing.T) {
	existing := &caldav.CalendarObject{ETag: "abc"}
	cases := []struct {
		name     string
		existing *caldav.CalendarObject
		opts     *caldav.PutCalendarObjectOptions
		wantFail bool
	}{
		{"no options", existing, nil, false},
		{"no headers", existing, &caldav.PutCalendarObjectOptions{}, false},
		{"create-only, nothing there", nil, &caldav.PutCalendarObjectOptions{IfNoneMatch: "*"}, false},
		{"create-only, exists", existing, &caldav.PutCalendarObjectOptions{IfNoneMatch: "*"}, true},
		{"if-none-match other etag", existing, &caldav.PutCalendarObjectOptions{IfNoneMatch: `"zzz"`}, false},
		{"if-none-match same etag", existing, &caldav.PutCalendarObjectOptions{IfNoneMatch: `"abc"`}, true},
		{"if-match same etag", existing, &caldav.PutCalendarObjectOptions{IfMatch: `"abc"`}, false},
		{"if-match stale etag", existing, &caldav.PutCalendarObjectOptions{IfMatch: `"old"`}, true},
		{"if-match wildcard, exists", existing, &caldav.PutCalendarObjectOptions{IfMatch: "*"}, false},
		{"if-match wildcard, missing", nil, &caldav.PutCalendarObjectOptions{IfMatch: "*"}, true},
		{"if-match etag, missing", nil, &caldav.PutCalendarObjectOptions{IfMatch: `"abc"`}, true},
	}
	for _, c := range cases {
		err := checkPreconditions(c.existing, c.opts)
		if (err != nil) != c.wantFail {
			t.Errorf("%s: err = %v, wantFail = %v", c.name, err, c.wantFail)
			continue
		}
		if err != nil && !strings.HasPrefix(err.Error(), "412 ") {
			t.Errorf("%s: err = %v, want a 412", c.name, err)
		}
	}
}
