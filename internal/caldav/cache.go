package caldav

import (
	"context"
	"fmt"
	"log"
	"maps"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/mrk1987/ioxide/internal/protonmail"
)

// The backend keeps a decrypted copy of every calendar in memory and serves
// reads from it. Listing a calendar from Proton takes minutes for a few
// thousand events, so after the first full load the backend follows the
// calendar's change log instead, which costs one small request per poll.
// Writes patch the cache directly. A full reload still happens when the
// change log says so, and once a day as a safety net.

const (
	syncInterval        = time.Minute
	listInterval        = 15 * time.Minute
	fullRefreshInterval = 24 * time.Hour
)

// cachedEvent is one Proton event, decrypted. The served resources (objs)
// are derived from these by rebuild, which merges a series' exceptions into
// the series' resource.
type cachedEvent struct {
	lastEdit int64
	uid      string
	rid      int64 // RECURRENCE-ID as Unix time; 0 for a master or plain event
	co       caldav.CalendarObject
}

func newCachedEvent(lastEdit int64, co *caldav.CalendarObject) cachedEvent {
	ce := cachedEvent{lastEdit: lastEdit, co: *co}
	if ev := firstEvent(co); ev != nil {
		ce.uid = eventUID(ev)
		ce.rid = recurrenceID(ev)
	}
	return ce
}

type calCache struct {
	refreshMu sync.Mutex // serialises refresh and sync of this calendar

	mu       sync.RWMutex
	loaded   bool
	fetched  time.Time                        // last full load
	changeID string                           // position in Proton's change log
	objs     map[string]caldav.CalendarObject // served resources, by path; derived by rebuild
	events   map[string]cachedEvent           // Proton events, by event id

	// Calendar keys and settings, fetched when first needed.
	keys     openpgp.KeyRing
	settings protonmail.CalendarSettings
}

func (b *backend) logf(format string, args ...any) {
	log.Printf("caldav: "+format, args...)
}

func (b *backend) cacheFor(calID string) *calCache {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	c, ok := b.cals[calID]
	if !ok {
		c = &calCache{objs: map[string]caldav.CalendarObject{}, events: map[string]cachedEvent{}}
		b.cals[calID] = c
	}
	return c
}

// calendarKeys returns the calendar's decrypted keys and settings, fetching
// them from Proton the first time.
func (b *backend) calendarKeys(c *calCache, calID string) (openpgp.KeyRing, protonmail.CalendarSettings, error) {
	c.mu.RLock()
	keys, settings := c.keys, c.settings
	c.mu.RUnlock()
	if keys != nil {
		return keys, settings, nil
	}

	bootstrap, err := b.client.BootstrapCalendar(calID)
	if err != nil {
		return nil, settings, fmt.Errorf("bootstrapping calendar %s: %w", calID, err)
	}
	keys, err = bootstrap.DecryptKeyring(b.privateKeys)
	if err != nil {
		return nil, settings, fmt.Errorf("decrypting keys of calendar %s: %w", calID, err)
	}

	c.mu.Lock()
	c.keys, c.settings = keys, bootstrap.CalendarSettings
	c.mu.Unlock()
	return keys, bootstrap.CalendarSettings, nil
}

func (c *calCache) forgetKeys() {
	c.mu.Lock()
	c.keys = nil
	c.mu.Unlock()
}

// refresh downloads the whole calendar and rebuilds its cache, reusing
// previously decrypted events whose LastEditTime is unchanged. On error the
// old data stays.
func (b *backend) refresh(calID string) error {
	c := b.cacheFor(calID)
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return b.refreshLocked(c, calID)
}

func (b *backend) refreshLocked(c *calCache, calID string) error {
	start := time.Now()

	// Take the change-log position before listing, so nothing that happens
	// during the listing is missed; applying a change twice is harmless.
	changeID, err := b.client.GetLatestCalendarChangeID(calID)
	if err != nil {
		return err
	}
	c.forgetKeys()
	calKeys, settings, err := b.calendarKeys(c, calID)
	if err != nil {
		return err
	}
	events, err := b.client.ListCalendarEvents(calID)
	if err != nil {
		return err
	}

	c.mu.RLock()
	old := maps.Clone(c.events) // store and evict may change c.events meanwhile
	c.mu.RUnlock()

	evs := make(map[string]cachedEvent, len(events))
	reused := 0
	for _, event := range events {
		if prev, ok := old[event.ID]; ok && prev.lastEdit == int64(event.LastEditTime) {
			evs[event.ID] = prev
			reused++
			continue
		}
		co, err := b.calendarObject(calID, calKeys, event, settings)
		if err != nil {
			b.logf("skipping unreadable event: %v", err)
			continue
		}
		evs[event.ID] = newCachedEvent(int64(event.LastEditTime), co)
	}

	c.mu.Lock()
	c.events, c.loaded, c.fetched, c.changeID = evs, true, time.Now(), changeID
	c.rebuild()
	n := len(c.objs)
	c.mu.Unlock()
	b.markDirty()
	b.logf("calendar %s: %d events (%d reused) in %s", calID, n, reused, time.Since(start).Round(time.Millisecond))
	return nil
}

// sync applies the changes Proton recorded since the last position. It falls
// back to a full refresh when the log cannot be followed.
func (b *backend) sync(calID string) error {
	c := b.cacheFor(calID)
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	c.mu.RLock()
	loaded, changeID := c.loaded, c.changeID
	c.mu.RUnlock()
	if !loaded || changeID == "" {
		return b.refreshLocked(c, calID)
	}

	applied := 0
	for {
		changes, err := b.client.GetCalendarChanges(calID, changeID)
		if err != nil {
			return err
		}
		if changes.Refresh != 0 || changes.KeysOrSettingsChanged() {
			b.logf("calendar %s: change log asks for a full reload", calID)
			return b.refreshLocked(c, calID)
		}

		n, err := b.applyChanges(c, calID, changes.CalendarEvents)
		applied += n
		if err != nil {
			return err
		}

		changeID = changes.ChangeID
		c.mu.Lock()
		c.changeID = changeID
		c.mu.Unlock()
		if changes.More == 0 {
			break
		}
	}
	if applied > 0 {
		b.markDirty()
		b.logf("calendar %s: applied %d changes", calID, applied)
	}
	return nil
}

// applyChanges updates the cache from change-log entries. The log carries
// only event metadata, so changed events are fetched and decrypted one by
// one. It returns how many entries were applied.
func (b *backend) applyChanges(c *calCache, calID string, changes []protonmail.CalendarEventChange) (int, error) {
	applied := 0
	for _, change := range changes {
		switch change.Action {
		case protonmail.ChangeDelete:
			b.evict(calID, change.ID)
			applied++
		case protonmail.ChangeCreate, protonmail.ChangeUpdate:
			calKeys, settings, err := b.calendarKeys(c, calID)
			if err != nil {
				return applied, err
			}
			event, err := b.client.GetCalendarEvent(calID, change.ID)
			if err != nil {
				return applied, fmt.Errorf("fetching changed event %s/%s: %w", calID, change.ID, err)
			}
			co, err := b.calendarObject(calID, calKeys, event, settings)
			if err != nil {
				b.logf("skipping unreadable event: %v", err)
				continue
			}
			b.store(calID, event.ID, int64(event.LastEditTime), co)
			applied++
		}
	}
	return applied, nil
}

// objects returns the cached objects, loading the calendar on first use.
func (b *backend) objects(calID string) ([]caldav.CalendarObject, error) {
	if !b.isLoaded(calID) {
		if err := b.refresh(calID); err != nil {
			return nil, err
		}
	}

	c := b.cacheFor(calID)
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]caldav.CalendarObject, 0, len(c.objs))
	for _, co := range c.objs {
		out = append(out, co)
	}
	return out, nil
}

// isLoaded reports whether the calendar has been fetched at least once.
func (b *backend) isLoaded(calID string) bool {
	c := b.cacheFor(calID)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}

// cachedEvent returns the cached copy of one Proton event, if any.
func (b *backend) cachedEvent(calID, eventID string) (*ical.Event, bool) {
	c := b.cacheFor(calID)
	c.mu.RLock()
	defer c.mu.RUnlock()
	ev, ok := c.events[eventID]
	if !ok {
		return nil, false
	}
	return firstEvent(&ev.co), true
}

// lookup returns one cached object by path, if the calendar is loaded.
func (b *backend) lookup(calID, path string) (*caldav.CalendarObject, bool) {
	c := b.cacheFor(calID)
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

func (b *backend) store(calID, eventID string, lastEdit int64, co *caldav.CalendarObject) {
	c := b.cacheFor(calID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		return
	}
	c.events[eventID] = newCachedEvent(lastEdit, co)
	c.rebuild()
	b.markDirty()
}

func (b *backend) evict(calID, eventID string) {
	c := b.cacheFor(calID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.events[eventID]; ok {
		delete(c.events, eventID)
		c.rebuild()
		b.markDirty()
	}
}

// warm restores the on-disk cache, loads every calendar that is still
// missing, then keeps all of them current from Proton's change log.
func (b *backend) warm() {
	b.loadDiskCache()
	go b.saver()

	var calIDs []string
	var listedAt time.Time
	for {
		if time.Since(listedAt) >= listInterval {
			cals, err := b.ListCalendars(context.Background())
			if err != nil {
				b.logf("listing calendars: %v", err)
			} else {
				calIDs = calIDs[:0]
				for _, cal := range cals {
					if calID, ok := splitCalendarPath(cal.Path); ok {
						calIDs = append(calIDs, calID)
					}
				}
				listedAt = time.Now()
			}
		}

		for _, calID := range calIDs {
			c := b.cacheFor(calID)
			c.mu.RLock()
			stale := c.loaded && time.Since(c.fetched) >= fullRefreshInterval
			c.mu.RUnlock()

			var err error
			if stale {
				err = b.refresh(calID)
			} else {
				err = b.sync(calID)
			}
			if err != nil {
				b.logf("updating calendar %s: %v", calID, err)
			}
		}
		time.Sleep(syncInterval)
	}
}

// overlaps reports whether the object can fall inside [start, end). Recurring
// events and anything unparseable are always included: a superset is valid
// CalDAV, and clients filter again on their side.
func overlaps(co caldav.CalendarObject, start, end time.Time) bool {
	if start.IsZero() && end.IsZero() {
		return true
	}
	if co.Data == nil {
		return true
	}
	for _, ev := range co.Data.Events() {
		if ev.Props.Get(ical.PropRecurrenceRule) != nil ||
			ev.Props.Get(ical.PropRecurrenceDates) != nil ||
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
