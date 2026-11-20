package auth

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrk1987/ioxide/internal/protonmail"
	"github.com/mrk1987/ioxide/internal/prototest"
)

// storedRecord decrypts username's stored entry the way openSession does.
func storedRecord(t *testing.T, username string, key *[keySize]byte) CachedAuth {
	t.Helper()
	store, err := loadStore()
	if err != nil {
		t.Fatal(err)
	}
	cleartext, err := decrypt(store[username], key)
	if err != nil {
		t.Fatal(err)
	}
	var cached CachedAuth
	if err := json.Unmarshal(cleartext, &cached); err != nil {
		t.Fatal(err)
	}
	return cached
}

func clientFor(account *prototest.Account) func() *protonmail.Client {
	return func() *protonmail.Client { return &protonmail.Client{RootURL: account.URL, AppVersion: "test"} }
}

func TestLegacyRecordIsRewrittenWithoutPasswords(t *testing.T) {
	useTempConfigDir(t)
	account := prototest.NewAccount(t, "mailbox-password")
	salts, err := clientFor(account)().ListKeySalts()
	if err != nil {
		t.Fatal(err)
	}
	key, password, _ := GeneratePassword()
	legacy := &CachedAuth{
		Auth:            protonmail.Auth{UID: "uid", AccessToken: "access", RefreshToken: "refresh"},
		LoginPassword:   "login-password",
		MailboxPassword: account.Password,
		KeySalts:        salts,
	}
	if err := EncryptAndSave(legacy, "user", key); err != nil {
		t.Fatal(err)
	}

	if _, err := NewManager(clientFor(account)).Auth("user", password); err != nil {
		t.Fatalf("legacy record did not open: %v", err)
	}
	got := storedRecord(t, "user", key)
	if got.LoginPassword != "" || got.MailboxPassword != "" || got.KeySalts != nil {
		t.Fatal("passwords are still stored after migration")
	}
	if len(got.KeyPassphrases) != len(account.KeyPasswords) {
		t.Fatalf("stored %d key passphrases, want %d", len(got.KeyPassphrases), len(account.KeyPasswords))
	}

	// The migrated record must open again on its own.
	if _, err := NewManager(clientFor(account)).Auth("user", password); err != nil {
		t.Fatalf("migrated record did not open: %v", err)
	}
}

func TestNewSignInStoresNoPassword(t *testing.T) {
	account := prototest.NewAccount(t, "mailbox-password")
	cached, err := NewCachedAuth(clientFor(account)(), &protonmail.Auth{UID: "uid", AccessToken: "access"}, account.Password)
	if err != nil {
		t.Fatal(err)
	}
	cleartext, _ := json.Marshal(cached)
	for _, field := range []string{"LoginPassword", "MailboxPassword", "KeySalts", account.Password} {
		if strings.Contains(string(cleartext), field) {
			t.Errorf("new record contains %q", field)
		}
	}
}

func TestEndedSessionAsksForSignIn(t *testing.T) {
	useTempConfigDir(t)
	account := prototest.NewAccount(t, "mailbox-password")
	account.RefreshInvalid.Store(true)
	cached, err := NewCachedAuth(clientFor(account)(), &protonmail.Auth{UID: "uid", AccessToken: "access"}, account.Password)
	if err != nil {
		t.Fatal(err)
	}
	cached.RefreshToken = "refresh"
	key, password, _ := GeneratePassword()
	if err := EncryptAndSave(cached, "user", key); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(clientFor(account)).Auth("user", password); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
}

func TestReplaceKeepsBridgePasswordAndRefusesAnother(t *testing.T) {
	useTempConfigDir(t)
	key, _, _ := GeneratePassword()
	if err := EncryptAndSave(&CachedAuth{Auth: protonmail.Auth{UID: "old"}}, "user", key); err != nil {
		t.Fatal(err)
	}
	if err := Replace(&CachedAuth{Auth: protonmail.Auth{UID: "new"}}, "user", key); err != nil {
		t.Fatal(err)
	}
	if got := storedRecord(t, "user", key); got.UID != "new" {
		t.Fatalf("UID = %q, want new", got.UID)
	}
	other, _, _ := GeneratePassword()
	if err := Replace(&CachedAuth{}, "user", other); err == nil {
		t.Fatal("Replace accepted a bridge password that does not open the entry")
	}
	if err := Replace(&CachedAuth{}, "missing", key); err == nil {
		t.Fatal("Replace created an account")
	}
}

// storeNewRecord saves a current-format record for the fake account.
func storeNewRecord(t *testing.T, account *prototest.Account, a protonmail.Auth) (*[keySize]byte, string) {
	t.Helper()
	cached, err := NewCachedAuth(clientFor(account)(), &protonmail.Auth{UID: "uid", AccessToken: "access"}, account.Password)
	if err != nil {
		t.Fatal(err)
	}
	cached.Auth = a
	key, password, _ := GeneratePassword()
	if err := EncryptAndSave(cached, "user", key); err != nil {
		t.Fatal(err)
	}
	return key, password
}

func TestRotatedRefreshTokenIsSavedEvenWhenUnlockFails(t *testing.T) {
	useTempConfigDir(t)
	account := prototest.NewAccount(t, "mailbox-password")
	key, password := storeNewRecord(t, account, protonmail.Auth{UID: "uid", AccessToken: "access", RefreshToken: "refresh"})
	account.UsersStatus.Store(503) // refresh succeeds, then Proton has an outage

	if _, err := NewManager(clientFor(account)).Auth("user", password); err == nil {
		t.Fatal("open succeeded during the outage")
	}
	// The old refresh token is invalid now; losing the new one would force a sign-in.
	if got := storedRecord(t, "user", key); got.RefreshToken != "refresh-2" {
		t.Fatalf("stored refresh token = %q, want the rotated one", got.RefreshToken)
	}
}

func TestExpiredAccessTokenOnlySessionEndsWithoutHanging(t *testing.T) {
	useTempConfigDir(t)
	account := prototest.NewAccount(t, "mailbox-password")
	_, password := storeNewRecord(t, account, protonmail.Auth{UID: "uid", AccessToken: "access"})
	account.UsersStatus.Store(401)

	done := make(chan error, 1)
	go func() { _, err := NewManager(clientFor(account)).Auth("user", password); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("err = %v, want ErrSessionExpired", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("opening an expired access-token-only session hung")
	}
}

func TestReplaceRefusesADifferentProtonAccount(t *testing.T) {
	useTempConfigDir(t)
	key, _, _ := GeneratePassword()
	if err := EncryptAndSave(&CachedAuth{Auth: protonmail.Auth{UserID: "alice"}}, "user", key); err != nil {
		t.Fatal(err)
	}
	if err := Replace(&CachedAuth{Auth: protonmail.Auth{UserID: "mallory"}}, "user", key); err == nil {
		t.Fatal("Replace moved a bridge password to a different Proton account")
	}
	if err := Replace(&CachedAuth{Auth: protonmail.Auth{UserID: "alice"}}, "user", key); err != nil {
		t.Fatalf("same account refused: %v", err)
	}
}
