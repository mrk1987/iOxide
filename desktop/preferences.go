package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	bridgeui "github.com/mrk1987/ioxide/internal/desktop"
)

// autostartFlag marks a launch made by the login item, so a manual launch
// always opens the window.
const autostartFlag = "--autostart"

// preferences is the desktop app's non-secret settings file.
type preferences struct {
	URL            string `json:"url"`
	StartMinimized bool   `json:"startMinimized"`
	// StartupDefaultsApplied records that start at login and start minimized
	// were switched on once after the first account was set up. After that
	// the user's own choice is never overridden.
	StartupDefaultsApplied bool `json:"startupDefaultsApplied"`
}

var preferencesMu sync.Mutex

// preferencesPath is a variable so tests can point it at a temporary folder.
var preferencesPath = func() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "ioxide", "desktop.json"), nil
}

// loadPreferences returns zero values when the file does not exist yet.
// Callers must hold preferencesMu when they intend to write the result back.
func loadPreferences() (preferences, error) {
	var prefs preferences
	path, err := preferencesPath()
	if err != nil {
		return prefs, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return prefs, nil
	}
	if err != nil {
		return prefs, err
	}
	if err := json.Unmarshal(data, &prefs); err != nil {
		return preferences{}, err
	}
	return prefs, nil
}

func savePreferences(prefs preferences) error {
	path, err := preferencesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".desktop-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// updatePreferences reads, changes and writes the file under one lock, so
// fields written by different settings never overwrite each other.
func updatePreferences(change func(*preferences)) error {
	preferencesMu.Lock()
	defer preferencesMu.Unlock()
	prefs, err := loadPreferences()
	if err != nil {
		// A damaged file must not block saving a setting; start again from defaults.
		prefs = preferences{}
	}
	change(&prefs)
	return savePreferences(prefs)
}

// claimStartupDefaults turns on start minimized the first time an account is
// ready and reports whether start at login should be switched on now. It
// returns false on every later call, so a user who turns either option off
// keeps it off.
func claimStartupDefaults(state bridgeui.State) (bool, error) {
	ready := false
	for _, account := range state.Accounts {
		if !account.Locked {
			ready = true
			break
		}
	}
	if !ready {
		return false, nil
	}
	preferencesMu.Lock()
	defer preferencesMu.Unlock()
	prefs, err := loadPreferences()
	if err != nil {
		prefs = preferences{}
	}
	if prefs.StartupDefaultsApplied {
		return false, nil
	}
	prefs.StartMinimized = true
	prefs.StartupDefaultsApplied = true
	if err := savePreferences(prefs); err != nil {
		return false, err
	}
	return true, nil
}

// shouldStartHidden decides whether this launch keeps the window closed.
// Only a login-item launch with the setting on and at least one account set
// up starts in the tray; anything else would leave a new user with no window.
func shouldStartHidden(launchedAtLogin bool, prefs preferences, accounts int) bool {
	return launchedAtLogin && prefs.StartMinimized && accounts > 0
}

// needsAttention reports a problem that only the user can fix: the bridge
// could not start, or an account has no saved bridge password (locked, with
// the unlock prompt set once loading has finished). A failed resume is left
// out: its key is loaded, so the account is not locked, and at login the
// network is often not up yet.
func needsAttention(state bridgeui.State) bool {
	if state.Error != "" {
		return true
	}
	for _, account := range state.Accounts {
		if account.Locked && account.Error != "" {
			return true
		}
	}
	return false
}
