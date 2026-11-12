package protonmail

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// fakeProton serves a calendar event listing of n events in pages, and a
// small change log.
func fakeProton(t *testing.T, n int) (*Client, *int32) {
	t.Helper()
	var mu sync.Mutex
	var inFlight, maxInFlight int32

	mux := http.NewServeMux()
	mux.HandleFunc("GET /calendar/v1/cal1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		defer func() { mu.Lock(); inFlight--; mu.Unlock() }()

		page, _ := strconv.Atoi(r.URL.Query().Get("Page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("PageSize"))
		if size != eventPageSize {
			t.Errorf("PageSize = %d, want %d", size, eventPageSize)
		}
		var events []map[string]any
		for i := page * size; i < (page+1)*size && i < n; i++ {
			events = append(events, map[string]any{"ID": fmt.Sprintf("ev%04d", i), "LastEditTime": i})
		}
		json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "Events": events})
	})
	mux.HandleFunc("GET /calendar/v1/cal1/modelevents/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "CalendarModelEventID": "pos-1"})
	})
	mux.HandleFunc("GET /calendar/v1/cal1/modelevents/pos-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"Code": 1000, "CalendarModelEventID": "pos-2", "More": 0,
			"CalendarEvents": []map[string]any{
				{"ID": "gone", "Action": 0},
				{"ID": "new", "Action": 1, "Event": map[string]any{"ID": "new", "LastEditTime": 5}},
			},
			"CalendarSettings": []map[string]any{{"ID": "s"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{RootURL: srv.URL, AppVersion: "test"}, &maxInFlight
}

func TestListCalendarEventsFetchesEveryPageInOrder(t *testing.T) {
	for _, n := range []int{0, 1, 99, 100, 101, 650, 1000} {
		c, maxInFlight := fakeProton(t, n)
		events, err := c.ListCalendarEvents("cal1")
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if len(events) != n {
			t.Fatalf("n=%d: got %d events", n, len(events))
		}
		for i, ev := range events {
			if ev.ID != fmt.Sprintf("ev%04d", i) {
				t.Fatalf("n=%d: event %d is %s, order lost", n, i, ev.ID)
			}
		}
		if n > 400 && *maxInFlight < 2 {
			t.Errorf("n=%d: pages were never fetched in parallel", n)
		}
	}
}

func TestGetCalendarChanges(t *testing.T) {
	c, _ := fakeProton(t, 0)
	id, err := c.GetLatestCalendarChangeID("cal1")
	if err != nil || id != "pos-1" {
		t.Fatalf("GetLatestCalendarChangeID = %q, %v", id, err)
	}
	changes, err := c.GetCalendarChanges("cal1", id)
	if err != nil {
		t.Fatal(err)
	}
	if changes.ChangeID != "pos-2" || changes.More != 0 {
		t.Errorf("ChangeID = %q, More = %d", changes.ChangeID, changes.More)
	}
	if !changes.KeysOrSettingsChanged() {
		t.Error("settings change not detected")
	}
	if len(changes.CalendarEvents) != 2 || changes.CalendarEvents[0].Action != ChangeDelete ||
		changes.CalendarEvents[1].Action != ChangeCreate || changes.CalendarEvents[1].Event.ID != "new" {
		t.Errorf("CalendarEvents = %+v", changes.CalendarEvents)
	}
}
