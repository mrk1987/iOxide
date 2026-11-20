package protonmail

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mrk1987/ioxide/internal/prototest"
)

func TestUnlockStoresDerivedPassphrasesNotThePassword(t *testing.T) {
	account := prototest.NewAccount(t, "mailbox-password")
	c := &Client{RootURL: account.URL, AppVersion: "test"}
	auth := &Auth{UID: "uid", AccessToken: "access"}

	salts, err := c.ListKeySalts()
	if err != nil {
		t.Fatal(err)
	}
	keys, passphrases, err := c.UnlockWithPassword(auth, salts, account.Password)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("no keys unlocked")
	}
	for id, want := range account.KeyPasswords {
		got := passphrases[id]
		if !bytes.Equal(got, want) {
			t.Errorf("passphrase for %s is not the derived key password", id)
		}
		if bytes.Contains(got, []byte(account.Password)) {
			t.Errorf("passphrase for %s contains the mailbox password", id)
		}
	}

	if _, err := c.UnlockWithKeyPassphrases(auth, passphrases); err != nil {
		t.Fatalf("stored passphrases do not unlock the keys: %v", err)
	}
}

func TestUnlockWithMissingPassphraseAsksForSignIn(t *testing.T) {
	account := prototest.NewAccount(t, "mailbox-password")
	c := &Client{RootURL: account.URL, AppVersion: "test"}
	_, err := c.UnlockWithKeyPassphrases(&Auth{UID: "uid", AccessToken: "access"}, KeyPassphrases{})
	if !errors.Is(err, ErrMissingKeyPassphrase) {
		t.Fatalf("err = %v, want ErrMissingKeyPassphrase", err)
	}
}

func TestUnlockWithWrongPasswordFails(t *testing.T) {
	account := prototest.NewAccount(t, "mailbox-password")
	c := &Client{RootURL: account.URL, AppVersion: "test"}
	salts, err := c.ListKeySalts()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.UnlockWithPassword(&Auth{UID: "uid", AccessToken: "access"}, salts, "wrong"); err == nil {
		t.Fatal("wrong mailbox password unlocked the keys")
	}
}

func TestUnsaltedKeyIsRefusedNotStored(t *testing.T) {
	account := prototest.NewAccount(t, "mailbox-password")
	account.UnsaltedKey.Store("user-key")
	c := &Client{RootURL: account.URL, AppVersion: "test"}
	salts, err := c.ListKeySalts()
	if err != nil {
		t.Fatal(err)
	}
	_, passphrases, err := c.UnlockWithPassword(&Auth{UID: "uid", AccessToken: "access"}, salts, account.Password)
	if !errors.Is(err, ErrUnsaltedKey) {
		t.Fatalf("err = %v, want ErrUnsaltedKey", err)
	}
	if passphrases != nil {
		t.Fatal("passphrases returned for an account that cannot be stored safely")
	}
}

func TestEndedSessionStaysEnded(t *testing.T) {
	account := prototest.NewAccount(t, "mailbox-password")
	account.UsersStatus.Store(401)
	c := &Client{RootURL: account.URL, AppVersion: "test", ReAuth: func() error { return ErrSessionExpired }}
	c.SetSession("uid", "expired")
	if _, err := c.GetCurrentUser(); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("first request: err = %v, want ErrSessionExpired", err)
	}
	// Proton would now answer without the token; the client must not forget why.
	account.UsersStatus.Store(0)
	if _, err := c.GetCurrentUser(); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("later request: err = %v, want ErrSessionExpired", err)
	}
}
