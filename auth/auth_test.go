package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrk1987/ioxide/protonmail"
)

// useTempConfigDir points os.UserConfigDir at a fresh directory.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", dir)
	case "darwin":
		t.Setenv("HOME", dir)
		dir = filepath.Join(dir, "Library", "Application Support")
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	return dir
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, password, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parsePassword(password)
	if err != nil {
		t.Fatalf("parsePassword: %v", err)
	}
	if *parsed != *key {
		t.Fatal("bridge password does not decode to the generated key")
	}

	sealed, err := encrypt([]byte("secret"), key)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := decrypt(sealed, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "secret" {
		t.Errorf("decrypted %q", opened)
	}

	other, _, _ := GeneratePassword()
	if _, err := decrypt(sealed, other); err == nil {
		t.Error("decrypting with the wrong key succeeded")
	}
	if _, err := decrypt("AAAA", key); err == nil {
		t.Error("decrypting data shorter than a nonce succeeded")
	}
}

func TestParsePasswordRejectsBadInput(t *testing.T) {
	for _, p := range []string{"", "not base64!", "c2hvcnQ="} {
		if _, err := parsePassword(p); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("parsePassword(%q) = %v, want ErrUnauthorized", p, err)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	useTempConfigDir(t)

	key, _, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	cached := &CachedAuth{
		Auth:            protonmail.Auth{UID: "uid", RefreshToken: "refresh"},
		MailboxPassword: "mailbox",
		KeySalts:        map[string][]byte{"k": []byte("salt")},
	}
	if err := EncryptAndSave(cached, "alice", key); err != nil {
		t.Fatal(err)
	}

	usernames, err := ListUsernames()
	if err != nil {
		t.Fatal(err)
	}
	if len(usernames) != 1 || usernames[0] != "alice" {
		t.Fatalf("ListUsernames = %v", usernames)
	}

	store, err := loadStore()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decrypt(store["alice"], key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plain, []byte("mailbox")) {
		t.Errorf("stored credentials do not round-trip: %s", plain)
	}

	if runtime.GOOS != "windows" {
		p, _ := authFilePath()
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("auth file permissions = %o, want 600", perm)
		}
	}
}

func TestLegacyAuthFileIsCopied(t *testing.T) {
	configHome := useTempConfigDir(t)
	legacyDir := filepath.Join(configHome, legacyConfigDirName)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, authFileName), []byte(`{"bob":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	usernames, err := ListUsernames()
	if err != nil {
		t.Fatal(err)
	}
	if len(usernames) != 1 || usernames[0] != "bob" {
		t.Fatalf("ListUsernames after migration = %v", usernames)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, authFileName)); err != nil {
		t.Errorf("legacy file should be left in place: %v", err)
	}
}

func TestDeriveCacheKey(t *testing.T) {
	a, _, _ := GeneratePassword()
	b, _, _ := GeneratePassword()
	ka1, err := deriveCacheKey(a)
	if err != nil {
		t.Fatal(err)
	}
	ka2, _ := deriveCacheKey(a)
	kb, _ := deriveCacheKey(b)
	if ka1 != ka2 {
		t.Error("cache key is not deterministic")
	}
	if ka1 == kb {
		t.Error("different bridge passwords gave the same cache key")
	}
	if ka1 == *a {
		t.Error("cache key must differ from the bridge key")
	}
}

func TestManagerRejectsUnknownUser(t *testing.T) {
	useTempConfigDir(t)
	m := NewManager(func() *protonmail.Client { return &protonmail.Client{} })
	_, password, _ := GeneratePassword()
	if _, err := m.Auth("nobody", password); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Auth for unknown user = %v, want ErrUnauthorized", err)
	}
	if _, err := m.Auth("nobody", "garbage"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Auth with malformed password = %v, want ErrUnauthorized", err)
	}
}
