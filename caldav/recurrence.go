package caldav

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
)

// Proton stores a modified occurrence of a repeating event as a separate
// event with a RECURRENCE-ID and the same UID as the series. CalDAV requires
// every component with one UID to be in one resource (RFC 4791 section 4.1),
// and Apple's clients get visibly confused when that is not the case: the
// occurrence appears and disappears. So the cache groups Proton events by UID,
// serves the series and its exceptions as one resource at the series' path,
// and splits a written resource back into Proton events.

// recurrenceID returns the occurrence an exception overrides, as a Unix time,
// or 0 for a series master or a plain event. A RECURRENCE-ID the parser
// cannot read still marks an exception; it gets a stable non-zero id derived
// from its text.
func recurrenceID(ev *ical.Event) int64 {
	prop := ev.Props.Get(ical.PropRecurrenceID)
	if prop == nil {
		return 0
	}
	t, err := prop.DateTime(time.UTC)
	if err != nil {
		h := fnv.New64a()
		h.Write([]byte(prop.Value))
		return int64(h.Sum64()>>1) | 1
	}
	return t.Unix()
}

func eventUID(ev *ical.Event) string {
	uid, _ := ev.Props.Text(ical.PropUID)
	return uid
}

// firstEvent returns the single VEVENT of a cached calendar object.
func firstEvent(co *caldav.CalendarObject) *ical.Event {
	if co.Data == nil {
		return nil
	}
	events := co.Data.Events()
	if len(events) == 0 {
		return nil
	}
	return &events[0]
}

// rebuild recomputes the served resources from the cached Proton events.
// Must be called with c.mu held for writing.
func (c *calCache) rebuild() {
	type group struct {
		master     *cachedEvent
		exceptions []*cachedEvent
	}
	groups := map[string]*group{}
	var order []string // deterministic output helps debugging

	for id := range c.events {
		ev := c.events[id]
		key := ev.uid
		if key == "" {
			key = "id:" + id // no UID: serve on its own
		}
		g, ok := groups[key]
		if !ok {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		if ev.rid == 0 {
			if g.master == nil || ev.lastEdit > g.master.lastEdit {
				g.master = &ev
			}
		} else {
			g.exceptions = append(g.exceptions, &ev)
		}
	}

	objs := make(map[string]caldav.CalendarObject, len(c.events))
	for _, key := range order {
		g := groups[key]
		if g.master == nil {
			// Exceptions without a series on this account (for example an
			// invitation to one occurrence) are served on their own.
			for _, ex := range g.exceptions {
				objs[ex.co.Path] = ex.co
			}
			continue
		}
		if len(g.exceptions) == 0 {
			objs[g.master.co.Path] = g.master.co
			continue
		}
		objs[g.master.co.Path] = mergeSeries(g.master, g.exceptions)
	}
	c.objs = objs
}

// mergeSeries returns one resource holding the series master and all its
// exceptions.
func mergeSeries(master *cachedEvent, exceptions []*cachedEvent) caldav.CalendarObject {
	sort.Slice(exceptions, func(i, j int) bool { return exceptions[i].rid < exceptions[j].rid })

	cal := ical.NewCalendar()
	for name, props := range master.co.Data.Props {
		cal.Props[name] = props
	}
	cal.Children = append(cal.Children, master.co.Data.Children...)

	modTime := master.co.ModTime
	var etag strings.Builder
	etag.WriteString(master.co.ETag)
	for _, ex := range exceptions {
		if ev := firstEvent(&ex.co); ev != nil {
			cal.Children = append(cal.Children, ev.Component)
		}
		if ex.co.ModTime.After(modTime) {
			modTime = ex.co.ModTime
		}
		fmt.Fprintf(&etag, "-%X", ex.lastEdit)
	}

	return caldav.CalendarObject{
		Path:    master.co.Path,
		ModTime: modTime,
		ETag:    etag.String(),
		Data:    cal,
	}
}

// exceptionIDs returns the Proton event ids of the exceptions of a series,
// by the occurrence they override.
func (b *backend) exceptionIDs(calID, uid string) map[int64]string {
	c := b.cacheFor(calID)
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := map[int64]string{}
	for id, ev := range c.events {
		if ev.uid == uid && ev.rid != 0 {
			ids[ev.rid] = id
		}
	}
	return ids
}
