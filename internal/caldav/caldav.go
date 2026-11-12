// Package caldav exposes Proton Calendar as a CalDAV collection tree.
//
// Every calendar in the account appears as a collection under
// /caldav/calendars/<calendar id>/, and every event as
// <calendar id>/<event id>.ics inside it.
package caldav

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/mrk1987/ioxide/internal/protonmail"
)

const (
	principalPath = "/caldav/"
	homeSetPath   = principalPath + "calendars/"
)

type backend struct {
	client      *protonmail.Client
	privateKeys openpgp.EntityList
	ownEmails   map[string]bool // addresses whose keys are in privateKeys

	keysMu     sync.Mutex
	authorKeys map[string]openpgp.EntityList // public keys of other event authors, by email

	cacheMu   sync.Mutex
	cals      map[string]*calCache
	listed    bool
	lastSync  time.Time
	syncError string

	// On-disk copy of the cache; see diskcache.go.
	cachePath string
	cacheKey  *[32]byte
	dirtyMu   sync.Mutex
	dirty     bool
}

// NewHandler returns an HTTP handler serving the account's calendars over
// CalDAV. Requests must already be authenticated: the handler trusts the
// client and keys it is given. The decrypted calendars are cached on disk
// under cacheKey; with a nil key nothing is written to disk.
func NewHandler(client *protonmail.Client, privateKeys openpgp.EntityList, username string, cacheKey *[32]byte) (http.Handler, error) {
	return NewHandlerContext(context.Background(), client, privateKeys, username, cacheKey)
}

// Handler owns a CalDAV handler and its background cache workers.
type Handler struct {
	http.Handler
	backend *backend
	cancel  context.CancelFunc
	done    chan struct{}
}

// Close cancels the workers and waits for their final encrypted cache save.
func (h *Handler) Close() { h.cancel(); <-h.done }

// CacheStatus contains measured cache state, not estimated progress.
type CacheStatus struct {
	Listed     bool      `json:"listed"`
	LastSync   time.Time `json:"lastSync"`
	SyncError  string    `json:"syncError"`
	Calendars  int       `json:"calendars"`
	Loaded     int       `json:"loaded"`
	Events     int       `json:"events"`
	LastLoaded time.Time `json:"lastLoaded"`
}

func (h *Handler) Status() CacheStatus {
	var status CacheStatus
	h.backend.cacheMu.Lock()
	defer h.backend.cacheMu.Unlock()
	status.Listed = h.backend.listed
	status.LastSync = h.backend.lastSync
	status.SyncError = h.backend.syncError
	status.Calendars = len(h.backend.cals)
	for _, c := range h.backend.cals {
		c.mu.RLock()
		if c.loaded {
			status.Loaded++
		}
		status.Events += len(c.events)
		if c.fetched.After(status.LastLoaded) {
			status.LastLoaded = c.fetched
		}
		c.mu.RUnlock()
	}
	return status
}

// NewHandlerContext ties cache workers to an owning server's lifetime.
func NewHandlerContext(ctx context.Context, client *protonmail.Client, privateKeys openpgp.EntityList, username string, cacheKey *[32]byte) (*Handler, error) {
	if len(privateKeys) == 0 {
		return nil, errors.New("caldav: no private key available")
	}

	b := &backend{
		client:      client,
		privateKeys: privateKeys,
		ownEmails:   identityEmails(privateKeys),
		authorKeys:  map[string]openpgp.EntityList{},
		cals:        map[string]*calCache{},
		cacheKey:    cacheKey,
	}
	if cacheKey != nil {
		p, err := cacheFilePath(username)
		if err != nil {
			b.logf("calendar cache disabled: %v", err)
		} else {
			b.cachePath = p
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	h := &Handler{Handler: withPropPatch(&caldav.Handler{Backend: b}), backend: b, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		var workers sync.WaitGroup
		workers.Add(1)
		go func() { defer workers.Done(); b.saver(ctx) }()
		b.warm(ctx)
		workers.Wait()
		b.saveDiskCache()
	}()
	return h, nil
}

func identityEmails(keys openpgp.EntityList) map[string]bool {
	emails := map[string]bool{}
	for _, entity := range keys {
		for _, identity := range entity.Identities {
			if identity.UserId != nil && identity.UserId.Email != "" {
				emails[strings.ToLower(identity.UserId.Email)] = true
			}
		}
	}
	return emails
}

// Path helpers. The layout is fixed, so no round trip to Proton is needed to
// map a request path to calendar and event ids.

func calendarPath(calID string) string {
	return homeSetPath + calID
}

func objectPath(calID, eventID string) string {
	return homeSetPath + calID + "/" + eventID + ".ics"
}

// splitCalendarPath returns the calendar id in a calendar collection path.
func splitCalendarPath(p string) (calID string, ok bool) {
	rest, found := strings.CutPrefix(p, homeSetPath)
	if !found {
		return "", false
	}
	calID = strings.TrimSuffix(rest, "/")
	if calID == "" || strings.Contains(calID, "/") {
		return "", false
	}
	return calID, true
}

// splitObjectPath returns the calendar and event ids in a calendar object path.
func splitObjectPath(p string) (calID, eventID string, ok bool) {
	rest, found := strings.CutPrefix(p, homeSetPath)
	if !found {
		return "", "", false
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".ics")
	calID, eventID, found = strings.Cut(rest, "/")
	if !found || calID == "" || eventID == "" || strings.Contains(eventID, "/") {
		return "", "", false
	}
	return calID, eventID, true
}

// errNotFound marks a 404 so callers can tell "missing" from "failed".
var errNotFound = errors.New("not found")

func badPath(p string) error {
	return webdav.NewHTTPError(http.StatusNotFound, fmt.Errorf("caldav: no resource at %q: %w", p, errNotFound))
}

// webdav.UserPrincipalBackend

func (b *backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return principalPath, nil
}

// caldav.Backend

func (b *backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	return homeSetPath, nil
}

func (b *backend) CreateCalendar(ctx context.Context, cal *caldav.Calendar) error {
	return webdav.NewHTTPError(http.StatusForbidden, errors.New("caldav: creating calendars is not supported"))
}

func (b *backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	protonCals, err := b.client.ListCalendars()
	if err != nil {
		return nil, fmt.Errorf("caldav: listing calendars: %w", err)
	}

	cals := make([]caldav.Calendar, 0, len(protonCals))
	for _, cal := range protonCals {
		c, err := b.toCalendar(cal)
		if err != nil {
			return nil, err
		}
		cals = append(cals, *c)
	}
	return cals, nil
}

func (b *backend) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	calID, ok := splitCalendarPath(p)
	if !ok {
		return nil, badPath(p)
	}

	protonCals, err := b.client.ListCalendars()
	if err != nil {
		return nil, fmt.Errorf("caldav: listing calendars: %w", err)
	}
	for _, cal := range protonCals {
		if cal.ID == calID {
			return b.toCalendar(cal)
		}
	}
	return nil, badPath(p)
}

func (b *backend) toCalendar(cal *protonmail.Calendar) (*caldav.Calendar, error) {
	view, err := protonmail.FindMemberViewFromKeyring(cal.Members, b.privateKeys)
	if err != nil {
		return nil, fmt.Errorf("caldav: calendar %s: %w", cal.ID, err)
	}
	return &caldav.Calendar{
		Path:                  calendarPath(cal.ID),
		Name:                  view.Name,
		Description:           view.Description,
		SupportedComponentSet: []string{ical.CompEvent},
	}, nil
}

func (b *backend) GetCalendarObject(ctx context.Context, p string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	calID, eventID, ok := splitObjectPath(p)
	if !ok {
		return nil, badPath(p)
	}
	if co, hit := b.lookup(calID, p); hit {
		return co, nil
	}
	return b.fetchCalendarObject(calID, eventID)
}

// fetchCalendarObject downloads and decrypts one event, bypassing the cache.
func (b *backend) fetchCalendarObject(calID, eventID string) (*caldav.CalendarObject, error) {
	event, err := b.client.GetCalendarEvent(calID, eventID)
	if err != nil {
		var apiErr *protonmail.APIError
		if errors.As(err, &apiErr) && apiErr.Code == protonmail.CodeNotFound {
			return nil, badPath(objectPath(calID, eventID))
		}
		return nil, fmt.Errorf("caldav: fetching event %s/%s: %w", calID, eventID, err)
	}

	calKeys, settings, err := b.calendarKeys(b.cacheFor(calID), calID)
	if err != nil {
		return nil, fmt.Errorf("caldav: %w", err)
	}
	return b.calendarObject(calID, calKeys, event, settings)
}

func (b *backend) ListCalendarObjects(ctx context.Context, p string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	calID, ok := splitCalendarPath(p)
	if !ok {
		return nil, badPath(p)
	}
	return b.objects(calID)
}

func (b *backend) QueryCalendarObjects(ctx context.Context, p string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	calID, ok := splitCalendarPath(p)
	if !ok {
		return nil, badPath(p)
	}

	// Only VEVENTs are served, so a query for anything else matches nothing.
	if query.CompFilter.Name != ical.CompCalendar {
		return nil, nil
	}
	var eventFilter *caldav.CompFilter
	for i := range query.CompFilter.Comps {
		if query.CompFilter.Comps[i].Name == ical.CompEvent {
			eventFilter = &query.CompFilter.Comps[i]
			break
		}
	}
	if eventFilter == nil {
		return nil, nil
	}

	all, err := b.objects(calID)
	if err != nil {
		return nil, err
	}
	matched := make([]caldav.CalendarObject, 0, len(all))
	for _, co := range all {
		if overlaps(co, eventFilter.Start, eventFilter.End) {
			matched = append(matched, co)
		}
	}
	return matched, nil
}

func (b *backend) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	calID, eventID, ok := splitObjectPath(p)
	if !ok {
		return nil, badPath(p)
	}

	// A resource holds one event, or a repeating series plus its modified
	// occurrences, each of which is a separate event at Proton.
	var master *ical.Event
	var exceptions []ical.Event
	for _, ev := range cal.Events() {
		if recurrenceID(&ev) == 0 {
			if master != nil {
				return nil, webdav.NewHTTPError(http.StatusBadRequest, errors.New("caldav: more than one series master in resource"))
			}
			master = &ev
		} else {
			exceptions = append(exceptions, ev)
		}
	}
	if master == nil {
		if len(exceptions) != 1 {
			return nil, webdav.NewHTTPError(http.StatusBadRequest, errors.New("caldav: resource has no series master"))
		}
		master, exceptions = &exceptions[0], nil // a lone occurrence, written as is
	}

	existing, err := b.existingObject(calID, eventID)
	if err != nil {
		return nil, err
	}
	if err := checkPreconditions(existing, opts); err != nil {
		return nil, err
	}

	// One batch to Proton with only what changed: the series itself if it
	// did, every occurrence that is new or different, and deletes for
	// occurrences that are no longer in the resource.
	uid := eventUID(master)
	known := b.exceptionIDs(calID, uid)
	var writes []protonmail.EventWrite
	unchanged := func(id string, ev *ical.Event) bool {
		if id == "" {
			return false
		}
		cached, ok := b.cachedEvent(calID, id)
		if !ok {
			return false
		}
		if sameEvent(cached, ev) {
			return true
		}
		log.Printf("caldav: writing %s/%s: changed %v", calID, id, changedProps(cached, ev))
		return false
	}
	masterWritten := !unchanged(eventID, master)
	if masterWritten {
		writes = append(writes, protonmail.EventWrite{ID: eventID, Event: master})
	}
	for i := range exceptions {
		rid := recurrenceID(&exceptions[i])
		id := known[rid]
		delete(known, rid)
		if !unchanged(id, &exceptions[i]) {
			writes = append(writes, protonmail.EventWrite{ID: id, Event: &exceptions[i]})
		}
	}
	var removed []string
	for _, id := range known {
		writes = append(writes, protonmail.EventWrite{ID: id})
		removed = append(removed, id)
	}

	masterPath := objectPath(calID, eventID)
	if len(writes) == 0 {
		if served, ok := b.lookup(calID, masterPath); ok {
			return served, nil // nothing changed
		}
	}

	written, err := b.client.SyncCalendarEvents(calID, writes, b.privateKeys)
	if err != nil {
		return nil, fmt.Errorf("caldav: writing %s/%s: %w", calID, eventID, err)
	}

	// Proton assigns its own ids to new events, so the resource may now live
	// at a different path than the one the client chose. The response carries
	// the stored events, so the cache is updated from it without re-reading.
	for _, id := range removed {
		b.evict(calID, id)
	}
	fallback := &caldav.CalendarObject{Path: masterPath, Data: cal}
	if masterWritten {
		b.evict(calID, eventID)
		master0 := written[0]
		fallback = &caldav.CalendarObject{
			Path:    objectPath(calID, master0.ID),
			ModTime: master0.LastEditTime.Time(),
			ETag:    etag(master0),
			Data:    cal,
		}
	}
	calKeys, settings, err := b.calendarKeys(b.cacheFor(calID), calID)
	if err != nil {
		log.Printf("caldav: %s written but cache not updated, refreshing calendar: %v", fallback.Path, err)
		go b.refresh(calID)
		return fallback, nil
	}
	for _, ev := range written {
		if ev == nil {
			continue // a delete
		}
		co, err := b.calendarObject(calID, calKeys, ev, settings)
		if err != nil {
			log.Printf("caldav: event %s/%s written but unreadable, refreshing calendar: %v", calID, ev.ID, err)
			go b.refresh(calID)
			return fallback, nil
		}
		b.store(calID, ev.ID, int64(ev.LastEditTime), co)
	}
	if served, ok := b.lookup(calID, fallback.Path); ok {
		return served, nil // the merged resource, if the series has exceptions
	}
	return fallback, nil
}

// existingObject returns the current object at a path, or nil if there is
// none. It consults the cache when the calendar is loaded and Proton
// otherwise.
func (b *backend) existingObject(calID, eventID string) (*caldav.CalendarObject, error) {
	if co, ok := b.lookup(calID, objectPath(calID, eventID)); ok {
		return co, nil
	}
	if b.isLoaded(calID) {
		return nil, nil
	}
	co, err := b.fetchCalendarObject(calID, eventID)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return co, err
}

// checkPreconditions enforces the If-Match and If-None-Match headers of a
// PUT, so a client never overwrites an edit it has not seen.
func checkPreconditions(existing *caldav.CalendarObject, opts *caldav.PutCalendarObjectOptions) error {
	if opts == nil {
		return nil
	}
	failed := func(why string) error {
		return webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("caldav: "+why))
	}

	if opts.IfNoneMatch.IsSet() && existing != nil {
		if opts.IfNoneMatch.IsWildcard() {
			return failed("resource already exists")
		}
		if ok, err := opts.IfNoneMatch.MatchETag(existing.ETag); err != nil || ok {
			return failed("resource matches If-None-Match")
		}
	}
	if opts.IfMatch.IsSet() {
		if existing == nil {
			return failed("resource does not exist")
		}
		if !opts.IfMatch.IsWildcard() {
			if ok, err := opts.IfMatch.MatchETag(existing.ETag); err != nil || !ok {
				return failed("resource has changed")
			}
		}
	}
	return nil
}

func (b *backend) DeleteCalendarObject(ctx context.Context, p string) error {
	calID, eventID, ok := splitObjectPath(p)
	if !ok {
		return badPath(p)
	}
	if err := b.client.DeleteCalendarEvent(calID, eventID); err != nil {
		var apiErr *protonmail.APIError
		if errors.As(err, &apiErr) && apiErr.Code == protonmail.CodeNotFound {
			b.evict(calID, eventID)
			return badPath(p)
		}
		return fmt.Errorf("caldav: deleting event %s/%s: %w", calID, eventID, err)
	}
	b.evict(calID, eventID)
	return nil
}
