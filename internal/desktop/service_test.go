package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrk1987/ioxide/internal/auth"
)

type memoryVault struct {
	mu          sync.Mutex
	keys        map[string]string
	setHook     func()
	deleteError error
}

func (v *memoryVault) Get(u string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	k, ok := v.keys[u]
	if !ok {
		return "", errors.New("missing")
	}
	return k, nil
}
func (v *memoryVault) Set(u, k string) error {
	if v.setHook != nil {
		v.setHook()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[u] = k
	return nil
}
func (v *memoryVault) Delete(u string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.deleteError != nil {
		return v.deleteError
	}
	delete(v.keys, u)
	return nil
}

func testService(t *testing.T) (*Service, *memoryVault) {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
	v := &memoryVault{keys: map[string]string{}}
	s := New(v)
	s.address = "127.0.0.1:0"
	t.Cleanup(s.Close)
	return s, v
}

func TestCancelledLoginDoesNotPersistAfterKeychainPrompt(t *testing.T) {
	s, vault := testService(t)
	entered, release := make(chan struct{}), make(chan struct{})
	vault.setHook = func() { close(entered); <-release }
	ctx, cancel := context.WithCancel(context.Background())
	p := &pendingLogin{ctx: ctx, cancel: cancel, username: "sample", password: "dummy-login", stage: "mailbox-password"}
	s.pending = p
	finished := make(chan error, 1)
	go func() { _, err := s.commit(p, &auth.CachedAuth{LoginPassword: "dummy-login"}); finished <- err }()
	<-entered
	s.CancelLogin()
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("cancelled login committed")
	}
	users, err := auth.ListUsernames()
	if err != nil || len(users) != 0 {
		t.Fatalf("stored accounts after cancellation: %v, %v", users, err)
	}
	if _, err := vault.Get("sample"); err == nil {
		t.Fatal("cancelled key remains in vault")
	}
}

func TestCommitCannotOverwriteConcurrentCLIAccount(t *testing.T) {
	s, vault := testService(t)
	key, _, err := auth.GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.EncryptAndSave(&auth.CachedAuth{LoginPassword: "existing-cli"}, "sample", key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &pendingLogin{ctx: ctx, cancel: cancel, username: "sample"}
	s.pending = p
	if _, err := s.commit(p, &auth.CachedAuth{LoginPassword: "replacement"}); err == nil {
		t.Fatal("existing account overwritten")
	}
	if _, err := vault.Get("sample"); err == nil {
		t.Fatal("failed commit left a keychain item")
	}
}

func TestSignInStagesAreEnforcedByBackend(t *testing.T) {
	s, _ := testService(t)
	if _, err := s.CompleteTOTP("123456"); err == nil {
		t.Fatal("accepted TOTP without pending login")
	}
	if _, err := s.CompleteMailboxPassword("dummy"); err == nil {
		t.Fatal("accepted mailbox password without pending login")
	}
	if _, err := s.VerificationURL(); err == nil {
		t.Fatal("returned a verification page without pending challenge")
	}
	if _, err := s.ContinueVerification(); err == nil {
		t.Fatal("continued verification without pending challenge")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.pending = &pendingLogin{ctx: ctx, cancel: cancel, stage: "two-factor"}
	if _, err := s.CompleteTOTP("12abcd"); err == nil {
		t.Fatal("accepted malformed TOTP")
	}
}

func TestStateNeverContainsPasswordsOrTokens(t *testing.T) {
	s, _ := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.pending = &pendingLogin{ctx: ctx, cancel: cancel, username: "sample", password: "sensitive-dummy-login", stage: "two-factor"}
	s.keys["sample"] = "sensitive-dummy-bridge"
	raw, err := json.Marshal(s.State())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sensitive-dummy") {
		t.Fatal("snapshot contains a password")
	}
}

func TestStartDoesNotTakeOverOccupiedPort(t *testing.T) {
	s, _ := testService(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s.address = listener.Addr().String()
	if err := s.Start(); err == nil {
		t.Fatal("started on occupied port")
	}
	if s.State().Running {
		t.Fatal("reported running after bind failure")
	}
	conn, err := net.DialTimeout("tcp", s.address, time.Second)
	if err != nil {
		t.Fatalf("foreign listener was affected: %v", err)
	}
	conn.Close()
}

func TestRemovalKeychainFailureKeepsAccountAndRestartsBridge(t *testing.T) {
	s, vault := testService(t)
	key, _, err := auth.GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.EncryptAndSave(&auth.CachedAuth{}, "sample", key); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	vault.deleteError = errors.New("credential store unavailable")
	s.mu.Lock()
	s.keys["sample"] = "dummy-bridge"
	s.mu.Unlock()
	if err := s.RemoveAccount("sample"); err == nil {
		t.Fatal("did not report failed keychain deletion")
	}
	if !s.State().Running {
		t.Fatal("remaining bridge was not restarted")
	}
	if len(s.State().Accounts) != 1 {
		t.Fatal("failed removal hid the account, preventing retry")
	}
}
