package main

import (
	"os"
	"path/filepath"
	"testing"

	bridgeui "github.com/mrk1987/ioxide/internal/desktop"
)

func usePreferencesDir(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ioxide", "desktop.json")
	previous := preferencesPath
	preferencesPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { preferencesPath = previous })
	return path
}

func readyState() bridgeui.State {
	return bridgeui.State{Accounts: []bridgeui.Account{{Username: "alex@example.com"}}}
}

func TestStartupDefaultsWaitForAUsableAccount(t *testing.T) {
	usePreferencesDir(t)
	for _, state := range []bridgeui.State{{}, {Accounts: []bridgeui.Account{{Username: "a", Locked: true}}}} {
		enable, err := claimStartupDefaults(state)
		if err != nil || enable {
			t.Fatalf("defaults applied before setup: enable=%v err=%v", enable, err)
		}
	}
	prefs, _ := loadPreferences()
	if prefs.StartMinimized || prefs.StartupDefaultsApplied {
		t.Fatalf("preferences changed before setup: %+v", prefs)
	}
}

func TestStartupDefaultsApplyOnce(t *testing.T) {
	usePreferencesDir(t)
	enable, err := claimStartupDefaults(readyState())
	if err != nil || !enable {
		t.Fatalf("first setup should enable start at login: enable=%v err=%v", enable, err)
	}
	prefs, _ := loadPreferences()
	if !prefs.StartMinimized || !prefs.StartupDefaultsApplied {
		t.Fatalf("start minimized not switched on: %+v", prefs)
	}
	// The user turns start minimized off; a second account must not undo that.
	if err := updatePreferences(func(p *preferences) { p.StartMinimized = false }); err != nil {
		t.Fatal(err)
	}
	enable, err = claimStartupDefaults(readyState())
	if err != nil || enable {
		t.Fatalf("defaults applied twice: enable=%v err=%v", enable, err)
	}
	if prefs, _ := loadPreferences(); prefs.StartMinimized {
		t.Fatal("user's choice was overridden")
	}
}

func TestChoiceBeforeSetupIsKept(t *testing.T) {
	usePreferencesDir(t)
	if err := (&Desktop{}).SetStartMinimized(false); err != nil {
		t.Fatal(err)
	}
	if enable, _ := claimStartupDefaults(readyState()); enable {
		t.Fatal("defaults overrode a setting chosen before setup")
	}
}

func TestConnectionURLKeepsOtherPreferences(t *testing.T) {
	usePreferencesDir(t)
	d := &Desktop{}
	if err := d.SetStartMinimized(true); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveConnectionURL("https://calendar.example/caldav/"); err != nil {
		t.Fatal(err)
	}
	if !d.StartMinimized() || d.ConnectionURL() != "https://calendar.example/caldav/" {
		t.Fatalf("settings lost: minimized=%v url=%q", d.StartMinimized(), d.ConnectionURL())
	}
}

func TestOldPreferencesFileStillReads(t *testing.T) {
	path := usePreferencesDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"url":"https://calendar.example/caldav/"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Desktop{}
	if d.ConnectionURL() != "https://calendar.example/caldav/" || d.StartMinimized() {
		t.Fatal("file written by an older build was misread")
	}
}

func TestShouldStartHidden(t *testing.T) {
	on := preferences{StartMinimized: true}
	cases := []struct {
		login    bool
		prefs    preferences
		accounts int
		want     bool
	}{
		{true, on, 1, true},
		{false, on, 1, false},           // a manual launch always shows the window
		{true, preferences{}, 1, false}, // setting off
		{true, on, 0, false},            // nothing set up yet
	}
	for _, c := range cases {
		if got := shouldStartHidden(c.login, c.prefs, c.accounts); got != c.want {
			t.Errorf("shouldStartHidden(%v, %+v, %d) = %v, want %v", c.login, c.prefs, c.accounts, got, c.want)
		}
	}
}

func TestNeedsAttention(t *testing.T) {
	if needsAttention(bridgeui.State{Running: true, Accounts: []bridgeui.Account{{Username: "a", Locked: true}}}) {
		t.Error("an account still loading is not a problem")
	}
	if needsAttention(bridgeui.State{Running: true, Accounts: []bridgeui.Account{{Username: "a", Error: "Could not resume the account."}}}) {
		t.Error("a failed resume at login is retried and must not open the window")
	}
	if !needsAttention(bridgeui.State{Running: true, Accounts: []bridgeui.Account{{Username: "a", Locked: true, Error: "Enter your bridge password to unlock this account."}}}) {
		t.Error("a locked account needs the window")
	}
	if !needsAttention(bridgeui.State{Error: "Port 8081 is already in use."}) {
		t.Error("a bridge that could not start needs the window")
	}
}
