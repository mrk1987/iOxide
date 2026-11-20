package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/mrk1987/ioxide/internal/auth"
	"github.com/mrk1987/ioxide/internal/protonmail"
	"github.com/mrk1987/ioxide/internal/prototest"
)

func waitForAccount(t *testing.T, s *Service, ok func(Account) bool) Account {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, account := range s.State().Accounts {
			if ok(account) {
				return account
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("account never reached the expected state: %+v", s.State().Accounts)
	return Account{}
}

func TestSignInAgainKeepsBridgePassword(t *testing.T) {
	s, vault := testService(t)
	account := prototest.NewAccount(t, "mailbox-password")
	s.newClient = func(ctx context.Context) *protonmail.Client {
		return &protonmail.Client{Context: ctx, RootURL: account.URL, AppVersion: "test"}
	}

	// A stored account whose Proton session has ended.
	cached, err := auth.NewCachedAuth(s.newClient(context.Background()), &protonmail.Auth{UID: "uid", AccessToken: "access"}, account.Password)
	if err != nil {
		t.Fatal(err)
	}
	cached.RefreshToken = "refresh"
	key, bridgePassword, _ := auth.GeneratePassword()
	if err := auth.EncryptAndSave(cached, "sample", key); err != nil {
		t.Fatal(err)
	}
	vault.keys["sample"] = bridgePassword
	account.RefreshInvalid.Store(true)

	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	waitForAccount(t, s, func(a Account) bool { return a.SignInAgain })
	if s.reusableKey("sample") != bridgePassword {
		t.Fatal("an ended session did not offer to keep the bridge password")
	}

	// The new sign-in has a fresh session; finish it as the sign-in flow would.
	account.RefreshInvalid.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p := &pendingLogin{ctx: ctx, cancel: cancel, username: "sample", reuseKey: bridgePassword, client: s.newClient(ctx), stage: "signing-in",
		auth: &protonmail.Auth{UID: "uid", AccessToken: "access-new", RefreshToken: "refresh-new"}}
	s.mu.Lock()
	s.pending = p
	s.mu.Unlock()
	if _, err := s.finish(p, account.Password); err != nil {
		t.Fatal(err)
	}

	if vault.keys["sample"] != bridgePassword {
		t.Fatal("signing in again changed the bridge password")
	}
	waitForAccount(t, s, func(a Account) bool { return !a.SignInAgain && a.Error == "" && a.Listed })
	revealed, err := s.RevealPassword("sample")
	if err != nil || revealed != bridgePassword {
		t.Fatalf("bridge password after sign-in = %q, %v", revealed, err)
	}
}

func TestHealthyAccountCannotBeSignedInOver(t *testing.T) {
	s, _ := testService(t)
	key, _, _ := auth.GeneratePassword()
	if err := auth.EncryptAndSave(&auth.CachedAuth{}, "sample", key); err != nil {
		t.Fatal(err)
	}
	if s.reusableKey("sample") != "" {
		t.Fatal("a healthy account offered its bridge password for a new sign-in")
	}
}
