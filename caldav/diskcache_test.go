package caldav

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/emersion/go-webdav/caldav"

	"github.com/mrk1987/ioxide/protonmail"
)

func testBackend(t *testing.T, key *[32]byte) *backend {
	t.Helper()
	b := &backend{cals: map[string]*calCache{}, cacheKey: key}
	if key != nil {
		b.cachePath = filepath.Join(t.TempDir(), "test.cache")
	}
	return b
}

func testKey(b byte) *[32]byte {
	var k [32]byte
	for i := range k {
		k[i] = b
	}
	return &k
}

// loadedCalendar marks a calendar as loaded with one event in it.
func loadedCalendar(t *testing.T, b *backend, calID, eventID string) caldav.CalendarObject {
	t.Helper()
	c := b.cacheFor(calID)
	c.mu.Lock()
	c.loaded, c.changeID, c.fetched = true, "pos-"+calID, time.Unix(1700000000, 0)
	c.mu.Unlock()
	co := obj(t, "DTSTART:20261104T100000Z\r\nDTEND:20261104T110000Z\r\nSUMMARY:cached")
	co.Path = objectPath(calID, eventID)
	co.ETag = "7A" + eventID
	co.ModTime = time.Unix(122, 0)
	b.store(calID, eventID, 122, &co)
	return co
}

func TestSnapshotRoundTrip(t *testing.T) {
	key := testKey(1)
	b := testBackend(t, key)
	loadedCalendar(t, b, "cal1", "ev1")
	loadedCalendar(t, b, "cal2", "ev2")
	b.cacheFor("unloaded") // never loaded, must not be saved

	b.saveDiskCache()
	info, err := os.Stat(b.cachePath)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("cache file permissions = %o, want 600", info.Mode().Perm())
	}

	restored := testBackend(t, key)
	restored.cachePath = b.cachePath
	restored.loadDiskCache()

	for _, calID := range []string{"cal1", "cal2"} {
		if !restored.isLoaded(calID) {
			t.Fatalf("%s not restored", calID)
		}
		c := restored.cacheFor(calID)
		if c.changeID != "pos-"+calID {
			t.Errorf("%s changeID = %q", calID, c.changeID)
		}
		objs, _ := restored.objects(calID)
		if len(objs) != 1 {
			t.Fatalf("%s has %d objects after restore", calID, len(objs))
		}
		summary, _ := objs[0].Data.Events()[0].Props.Text("SUMMARY")
		if summary != "cached" || objs[0].ETag != "7Aev"+calID[len(calID)-1:] {
			t.Errorf("%s restored object = path %s etag %s summary %q", calID, objs[0].Path, objs[0].ETag, summary)
		}
	}
	if restored.isLoaded("unloaded") {
		t.Error("an unloaded calendar came back as loaded")
	}
}

func TestSnapshotRejectsWrongKeyAndTampering(t *testing.T) {
	b := testBackend(t, testKey(1))
	loadedCalendar(t, b, "cal1", "ev1")
	b.saveDiskCache()
	sealed, err := os.ReadFile(b.cachePath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := openSnapshot(sealed, testKey(2)); err == nil {
		t.Error("snapshot opened with the wrong key")
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)/2] ^= 0xff
	if _, err := openSnapshot(tampered, testKey(1)); err == nil {
		t.Error("tampered snapshot was accepted")
	}
	if _, err := openSnapshot(sealed[:10], testKey(1)); err == nil {
		t.Error("truncated snapshot was accepted")
	}

	other := testBackend(t, testKey(2))
	other.cachePath = b.cachePath
	other.loadDiskCache()
	if other.isLoaded("cal1") {
		t.Error("cache restored with the wrong key")
	}
}

func TestSaveOnlyWhenDirty(t *testing.T) {
	b := testBackend(t, testKey(1))
	b.saveDiskCache()
	if _, err := os.Stat(b.cachePath); err == nil {
		t.Error("cache written although nothing changed")
	}
	loadedCalendar(t, b, "cal1", "ev1")
	b.saveDiskCache()
	if _, err := os.Stat(b.cachePath); err != nil {
		t.Error("cache not written after a change")
	}
}

func TestNoCacheKeyMeansNoFile(t *testing.T) {
	b := testBackend(t, nil)
	loadedCalendar(t, b, "cal1", "ev1")
	b.saveDiskCache()
	b.loadDiskCache()
	if b.cachePath != "" {
		t.Error("cache path set without a key")
	}
}

func TestApplyChangesDeletes(t *testing.T) {
	b := testBackend(t, nil)
	loadedCalendar(t, b, "cal1", "ev1")
	loadedCalendar(t, b, "cal1", "ev2")

	n, err := b.applyChanges(b.cacheFor("cal1"), "cal1", []protonmail.CalendarEventChange{
		{ID: "ev1", Action: protonmail.ChangeDelete},
		{ID: "unknown", Action: protonmail.ChangeDelete},
	})
	if err != nil || n != 2 {
		t.Fatalf("applyChanges = %d, %v", n, err)
	}
	if _, ok := b.lookup("cal1", objectPath("cal1", "ev1")); ok {
		t.Error("deleted event still served")
	}
	if _, ok := b.lookup("cal1", objectPath("cal1", "ev2")); !ok {
		t.Error("untouched event lost")
	}
}
