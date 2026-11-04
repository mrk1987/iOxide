package caldav

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

// Every CalDAV list/query used to re-download all events from Proton and
// PGP-decrypt each one (minutes for a few thousand events). The backend now
// keeps a decrypted copy of every calendar in memory, refreshes it in the
// background, and serves reads from it. Unchanged events are not decrypted again
// on refresh (keyed on LastEditTime). Writes patch the cache directly.

const refreshInterval = 5 * time.Minute

type cachedEvent struct {
	lastEdit int64
	co       caldav.CalendarObject
}

type calCache struct {
	refreshMu sync.Mutex // serialises refreshes of this calendar
	mu        sync.RWMutex
	loaded    bool
	fetched   time.Time
	objs      map[string]caldav.CalendarObject // by object path
	events    map[string]cachedEvent           // by event id
}

func (b *backend) cacheFor(calId string) *calCache {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	if b.cals == nil {
		b.cals = map[string]*calCache{}
	}
	c, ok := b.cals[calId]
	if !ok {
		c = &calCache{objs: map[string]caldav.CalendarObject{}, events: map[string]cachedEvent{}}
		b.cals[calId] = c
	}
	return c
}

// refresh downloads the calendar and rebuilds the cache, reusing previously
// decrypted events whose LastEditTime is unchanged. On error the old data stays.
func (b *backend) refresh(calId string) error {
	c := b.cacheFor(calId)
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	start := time.Now()
	events, err := b.c.ListCalendarEvents(calId, nil)
	if err != nil {
		return err
	}
	bootstrap, err := b.c.BootstrapCalendar(calId)
	if err != nil {
		return err
	}
	calKr, err := bootstrap.DecryptKeyring(b.privateKeys)
	if err != nil {
		return err
	}

	c.mu.RLock()
	old := c.events
	c.mu.RUnlock()

	objs := make(map[string]caldav.CalendarObject, len(events))
	evs := make(map[string]cachedEvent, len(events))
	reused := 0
	for i, event := range events {
		if prev, ok := old[event.ID]; ok && prev.lastEdit == int64(event.LastEditTime) {
			objs[prev.co.Path] = prev.co
			evs[event.ID] = prev
			reused++
			continue
		}
		co, err := getCalendarObject(b, calId, calKr, event, bootstrap.CalendarSettings)
		if err != nil {
			log.Printf("caldav/refresh: skipping unparseable event %d (id %s): %v", i, event.ID, err)
			continue
		}
		objs[co.Path] = *co
		evs[event.ID] = cachedEvent{lastEdit: int64(event.LastEditTime), co: *co}
	}

	c.mu.Lock()
	c.objs, c.events, c.loaded, c.fetched = objs, evs, true, time.Now()
	c.mu.Unlock()
	log.Printf("caldav/refresh: calendar %s: %d events (%d reused) in %s", calId, len(objs), reused, time.Since(start).Round(time.Millisecond))
	return nil
}

// objects returns the cached objects, loading the calendar on first use.
func (b *backend) objects(calId string) ([]caldav.CalendarObject, error) {
	c := b.cacheFor(calId)
	c.mu.RLock()
	loaded := c.loaded
	c.mu.RUnlock()
	if !loaded {
		if err := b.refresh(calId); err != nil {
			return nil, err
		}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]caldav.CalendarObject, 0, len(c.objs))
	for _, co := range c.objs {
		out = append(out, co)
	}
	return out, nil
}

// lookup returns one cached object by path, if the calendar is loaded.
func (b *backend) lookup(calId, path string) (*caldav.CalendarObject, bool) {
	c := b.cacheFor(calId)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.loaded {
		return nil, false
	}
	co, ok := c.objs[path]
	if !ok {
		return nil, false
	}
	return &co, true
}

func (b *backend) store(calId, evtId string, lastEdit int64, co *caldav.CalendarObject) {
	c := b.cacheFor(calId)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		return
	}
	if prev, ok := c.events[evtId]; ok {
		delete(c.objs, prev.co.Path)
	}
	c.objs[co.Path] = *co
	c.events[evtId] = cachedEvent{lastEdit: lastEdit, co: *co}
}

func (b *backend) evict(calId, evtId string) {
	c := b.cacheFor(calId)
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.events[evtId]; ok {
		delete(c.objs, prev.co.Path)
		delete(c.events, evtId)
	}
}

// warm loads every calendar at startup, then refreshes them periodically.
func (b *backend) warm() {
	for {
		cals, err := b.ListCalendars(context.Background())
		if err != nil {
			log.Printf("caldav/warm: listing calendars: %v", err)
		}
		for _, cal := range cals {
			calId := lastSegment(cal.Path)
			if err := b.refresh(calId); err != nil {
				log.Printf("caldav/warm: refresh %s: %v", calId, err)
			}
		}
		time.Sleep(refreshInterval)
	}
}

func lastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// overlaps reports whether the object can fall inside [start, end). Recurring
// events and anything unparseable are always included (a superset is valid
// CalDAV; clients re-filter).
func overlaps(co caldav.CalendarObject, start, end time.Time) bool {
	if start.IsZero() && end.IsZero() {
		return true
	}
	if co.Data == nil {
		return true
	}
	for _, ev := range co.Data.Events() {
		if ev.Props.Get(ical.PropRecurrenceRule) != nil || ev.Props.Get(ical.PropRecurrenceDates) != nil ||
			ev.Props.Get(ical.PropRecurrenceID) != nil {
			return true
		}
		s, err := ev.DateTimeStart(time.UTC)
		if err != nil {
			return true
		}
		e, err := ev.DateTimeEnd(time.UTC)
		if err != nil || e.Before(s) {
			e = s
		}
		if !end.IsZero() && !s.Before(end) {
			continue
		}
		if !start.IsZero() && e.Before(start) {
			continue
		}
		return true
	}
	return false
}
