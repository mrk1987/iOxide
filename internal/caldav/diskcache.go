package caldav

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"golang.org/x/crypto/nacl/secretbox"
)

// The decrypted calendars are saved to disk so a restart does not have to
// download and decrypt everything again. The file is encrypted with a key
// derived from the bridge password (NaCl secretbox: XSalsa20-Poly1305), so
// without the bridge password it is noise, and any tampering is detected.
// A file that cannot be opened, for whatever reason, is simply ignored and
// rebuilt from Proton.

const (
	snapshotVersion = 1
	snapshotNonce   = 24
	saveInterval    = 30 * time.Second
)

type snapshot struct {
	Version   int
	SavedAt   time.Time
	Calendars []snapshotCalendar
}

type snapshotCalendar struct {
	ID       string
	ChangeID string
	Fetched  time.Time
	Events   []snapshotEvent
}

type snapshotEvent struct {
	ID       string
	LastEdit int64
	ICS      string
}

// cacheFilePath returns where the cache for username lives. The name is a
// hash so the file system never shows the account name.
func cacheFilePath(username string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "ioxide")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(username))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".cache"), nil
}

func sealSnapshot(snap *snapshot, key *[32]byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(snap); err != nil {
		return nil, err
	}
	var nonce [snapshotNonce]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, err
	}
	return secretbox.Seal(nonce[:], buf.Bytes(), &nonce, key), nil
}

func openSnapshot(sealed []byte, key *[32]byte) (*snapshot, error) {
	if len(sealed) < snapshotNonce {
		return nil, errors.New("file too short")
	}
	var nonce [snapshotNonce]byte
	copy(nonce[:], sealed[:snapshotNonce])
	plain, ok := secretbox.Open(nil, sealed[snapshotNonce:], &nonce, key)
	if !ok {
		return nil, errors.New("cannot decrypt (wrong bridge password or damaged file)")
	}
	var snap snapshot
	if err := json.Unmarshal(plain, &snap); err != nil {
		return nil, err
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported cache version %d", snap.Version)
	}
	return &snap, nil
}

// saveSnapshot writes the sealed snapshot atomically with owner-only
// permissions.
func saveSnapshot(path string, sealed []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cache-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(sealed); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// snapshotOf serialises every loaded calendar.
func (b *backend) snapshotOf() *snapshot {
	b.cacheMu.Lock()
	cals := make(map[string]*calCache, len(b.cals))
	for id, c := range b.cals {
		cals[id] = c
	}
	b.cacheMu.Unlock()

	snap := &snapshot{Version: snapshotVersion, SavedAt: time.Now()}
	for id, c := range cals {
		c.mu.RLock()
		if !c.loaded {
			c.mu.RUnlock()
			continue
		}
		sc := snapshotCalendar{ID: id, ChangeID: c.changeID, Fetched: c.fetched}
		for eventID, ev := range c.events {
			var buf bytes.Buffer
			if err := ical.NewEncoder(&buf).Encode(ev.co.Data); err != nil {
				continue
			}
			sc.Events = append(sc.Events, snapshotEvent{ID: eventID, LastEdit: ev.lastEdit, ICS: buf.String()})
		}
		c.mu.RUnlock()
		snap.Calendars = append(snap.Calendars, sc)
	}
	return snap
}

// restoreSnapshot fills the in-memory cache from a snapshot. Calendars that
// are already loaded are left alone.
func (b *backend) restoreSnapshot(snap *snapshot) (calendars, events int) {
	for _, sc := range snap.Calendars {
		c := b.cacheFor(sc.ID)
		c.mu.Lock()
		if c.loaded {
			c.mu.Unlock()
			continue
		}
		for _, se := range sc.Events {
			cal, err := ical.NewDecoder(bytes.NewReader([]byte(se.ICS))).Decode()
			if err != nil {
				continue
			}
			co := caldav.CalendarObject{
				Path:    objectPath(sc.ID, se.ID),
				ModTime: time.Unix(se.LastEdit, 0),
				ETag:    fmt.Sprintf("%X%s", se.LastEdit, se.ID),
				Data:    cal,
			}
			c.events[se.ID] = newCachedEvent(se.LastEdit, &co)
			events++
		}
		c.changeID, c.fetched, c.loaded = sc.ChangeID, sc.Fetched, true
		c.rebuild()
		c.mu.Unlock()
		calendars++
	}
	return calendars, events
}

// loadDiskCache restores the calendars saved by a previous run, if any.
func (b *backend) loadDiskCache() {
	if b.cachePath == "" {
		return
	}
	sealed, err := os.ReadFile(b.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		b.logf("reading calendar cache: %v", err)
		return
	}
	snap, err := openSnapshot(sealed, b.cacheKey)
	if err != nil {
		b.logf("ignoring calendar cache: %v", err)
		return
	}
	cals, events := b.restoreSnapshot(snap)
	b.logf("restored %d calendars with %d events from cache saved %s ago", cals, events, time.Since(snap.SavedAt).Round(time.Second))
}

// markDirty notes that the in-memory cache changed and should be saved.
func (b *backend) markDirty() {
	b.dirtyMu.Lock()
	b.dirty = true
	b.dirtyMu.Unlock()
}

// saveDiskCache writes the cache if it changed since the last save.
func (b *backend) saveDiskCache() {
	if b.cachePath == "" {
		return
	}
	b.dirtyMu.Lock()
	dirty := b.dirty
	b.dirty = false
	b.dirtyMu.Unlock()
	if !dirty {
		return
	}

	sealed, err := sealSnapshot(b.snapshotOf(), b.cacheKey)
	if err != nil {
		b.logf("encoding calendar cache: %v", err)
		return
	}
	if err := saveSnapshot(b.cachePath, sealed); err != nil {
		b.logf("saving calendar cache: %v", err)
	}
}

// saver writes the cache to disk periodically while it keeps changing.
func (b *backend) saver(ctx context.Context) {
	ticker := time.NewTicker(saveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			b.saveDiskCache()
			return
		case <-ticker.C:
			b.saveDiskCache()
		}
	}
}
