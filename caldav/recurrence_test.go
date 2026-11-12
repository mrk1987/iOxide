package caldav

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

func vevent(t *testing.T, uid, body string) caldav.CalendarObject {
	t.Helper()
	src := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20260101T000000Z\r\n" +
		body + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return caldav.CalendarObject{Data: cal}
}

func storeEvent(t *testing.T, b *backend, calID, eventID string, lastEdit int64, co caldav.CalendarObject) {
	t.Helper()
	c := b.cacheFor(calID)
	c.mu.Lock()
	c.loaded = true
	c.mu.Unlock()
	co.Path = objectPath(calID, eventID)
	co.ETag = "E" + eventID
	co.ModTime = time.Unix(lastEdit, 0)
	b.store(calID, eventID, lastEdit, &co)
}

func TestSeriesAndExceptionAreServedAsOneResource(t *testing.T) {
	b := testBackend(t, nil)
	storeEvent(t, b, "cal", "master", 10, vevent(t, "series-1", "DTSTART:20261105T190000Z\r\nDTEND:20261105T200000Z\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly"))
	storeEvent(t, b, "cal", "moved", 20, vevent(t, "series-1", "RECURRENCE-ID:20261112T190000Z\r\nDTSTART:20261112T203000Z\r\nDTEND:20261112T213000Z\r\nSUMMARY:Weekly"))
	storeEvent(t, b, "cal", "other", 5, vevent(t, "plain-1", "DTSTART:20261112T100000Z\r\nDTEND:20261112T110000Z\r\nSUMMARY:Plain"))

	objs, err := b.objects("cal")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("served %d resources, want 2 (series+exception merged, plus one plain event)", len(objs))
	}
	if _, ok := b.lookup("cal", objectPath("cal", "moved")); ok {
		t.Error("exception is still served as its own resource")
	}

	series, ok := b.lookup("cal", objectPath("cal", "master"))
	if !ok {
		t.Fatal("series resource missing")
	}
	events := series.Data.Events()
	if len(events) != 2 {
		t.Fatalf("series resource has %d VEVENTs, want 2", len(events))
	}
	if recurrenceID(&events[0]) != 0 || recurrenceID(&events[1]) == 0 {
		t.Error("master must come first, then the exception")
	}
	if series.ModTime != time.Unix(20, 0) {
		t.Errorf("ModTime = %v, want that of the newest part", series.ModTime)
	}
	etagBefore := series.ETag

	// Editing the exception must change the resource's ETag.
	storeEvent(t, b, "cal", "moved", 30, vevent(t, "series-1", "RECURRENCE-ID:20261112T190000Z\r\nDTSTART:20261112T210000Z\r\nDTEND:20261112T220000Z\r\nSUMMARY:Weekly"))
	series, _ = b.lookup("cal", objectPath("cal", "master"))
	if series.ETag == etagBefore {
		t.Error("ETag unchanged after the exception changed")
	}

	// Deleting the exception collapses the resource back to the master alone.
	b.evict("cal", "moved")
	series, _ = b.lookup("cal", objectPath("cal", "master"))
	if n := len(series.Data.Events()); n != 1 {
		t.Errorf("series resource has %d VEVENTs after evicting the exception, want 1", n)
	}
}

func TestLoneExceptionIsServedOnItsOwn(t *testing.T) {
	b := testBackend(t, nil)
	storeEvent(t, b, "cal", "invite", 1, vevent(t, "theirs", "RECURRENCE-ID:20261112T190000Z\r\nDTSTART:20261112T190000Z\r\nDTEND:20261112T200000Z\r\nSUMMARY:One occurrence"))
	if _, ok := b.lookup("cal", objectPath("cal", "invite")); !ok {
		t.Error("exception without a series on this account must be served as is")
	}
}

func TestExceptionIDs(t *testing.T) {
	b := testBackend(t, nil)
	storeEvent(t, b, "cal", "master", 1, vevent(t, "s", "DTSTART:20261105T190000Z\r\nRRULE:FREQ=WEEKLY"))
	storeEvent(t, b, "cal", "ex1", 1, vevent(t, "s", "RECURRENCE-ID:20261112T190000Z\r\nDTSTART:20261112T200000Z"))
	storeEvent(t, b, "cal", "ex2", 1, vevent(t, "s", "RECURRENCE-ID:20261119T190000Z\r\nDTSTART:20261119T200000Z"))
	ids := b.exceptionIDs("cal", "s")
	rid := time.Date(2026, 11, 12, 19, 0, 0, 0, time.UTC).Unix()
	if len(ids) != 2 || ids[rid] != "ex1" {
		t.Errorf("exceptionIDs = %v", ids)
	}
}

func TestSnapshotKeepsSeriesMerged(t *testing.T) {
	key := testKey(3)
	b := testBackend(t, key)
	storeEvent(t, b, "cal", "master", 10, vevent(t, "series-1", "DTSTART:20261105T190000Z\r\nRRULE:FREQ=WEEKLY"))
	storeEvent(t, b, "cal", "moved", 20, vevent(t, "series-1", "RECURRENCE-ID:20261112T190000Z\r\nDTSTART:20261112T203000Z"))
	b.saveDiskCache()

	restored := testBackend(t, key)
	restored.cachePath = b.cachePath
	restored.loadDiskCache()
	objs, _ := restored.objects("cal")
	if len(objs) != 1 || len(objs[0].Data.Events()) != 2 {
		t.Errorf("after restore: %d resources, want 1 with 2 VEVENTs", len(objs))
	}
}
