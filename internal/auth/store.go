package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

const (
	configDirName = "ioxide"
	authFileName  = "auth.json"

	// legacyConfigDirName is where hydroxide, which iOxide was forked from,
	// keeps its auth file. It is copied over once so an existing login keeps
	// working after upgrading.
	legacyConfigDirName = "hydroxide"
)

// authFilePath returns the path of the auth file, creating its directory.
func authFilePath() (string, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(configHome, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	p := filepath.Join(dir, authFileName)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		legacy := filepath.Join(configHome, legacyConfigDirName, authFileName)
		if err := copyFile(legacy, p); err == nil {
			log.Printf("auth: copied existing credentials from %s", legacy)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("copying %s: %w", legacy, err)
		}
	}
	return p, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// loadStore reads the auth file: encrypted credentials by username. A missing
// file is an empty store.
func loadStore() (map[string]string, error) {
	p, err := authFilePath()
	if err != nil {
		return nil, fmt.Errorf("locating auth file: %w", err)
	}

	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("opening auth file: %w", err)
	}
	defer f.Close()

	store := map[string]string{}
	if err := json.NewDecoder(f).Decode(&store); err != nil {
		return nil, fmt.Errorf("reading auth file: %w", err)
	}
	return store, nil
}

// saveStore atomically replaces the auth file. It writes to a temporary file
// in the same directory and renames it over the target, so a crash mid-write
// leaves the previous file intact rather than truncated. A truncated auth
// file costs an interactive re-login, which is why this is worth the care.
func saveStore(store map[string]string) error {
	p, err := authFilePath()
	if err != nil {
		return fmt.Errorf("locating auth file: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(p), ".auth.json.tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary auth file: %w", err)
	}
	tmp := f.Name()
	fail := func(step string, err error) error {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("%s auth file: %w", step, err)
	}

	// The file holds a refresh token and the login password.
	if err := f.Chmod(0o600); err != nil {
		return fail("protecting", err)
	}
	if err := json.NewEncoder(f).Encode(store); err != nil {
		return fail("writing", err)
	}
	if err := f.Sync(); err != nil {
		return fail("flushing", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing auth file: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replacing auth file: %w", err)
	}
	return nil
}
