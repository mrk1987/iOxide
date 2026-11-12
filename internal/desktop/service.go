// Package desktop owns the UI-independent desktop bridge state machine.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mrk1987/ioxide/internal/auth"
	"github.com/mrk1987/ioxide/internal/bridge"
	"github.com/mrk1987/ioxide/internal/protonmail"
)

// Vault stores bridge keys in the operating system's credential store.
type Vault interface {
	Get(username string) (string, error)
	Set(username, password string) error
	Delete(username string) error
}

type Account struct {
	Username string `json:"username"`
	Locked   bool   `json:"locked"`
	Error    string `json:"error"`
	bridge.AccountStatus
}

type State struct {
	Running         bool      `json:"running"`
	Address         string    `json:"address"`
	Accounts        []Account `json:"accounts"`
	Stage           string    `json:"stage"`
	PendingUsername string    `json:"pendingUsername"`
	Error           string    `json:"error"`
}

type pendingLogin struct {
	ctx                context.Context
	cancel             context.CancelFunc
	client             *protonmail.Client
	username, password string
	auth               *protonmail.Auth
	stage              string
	challenge          *protonmail.HumanVerificationDetails
	verifyURL          string
}

// Service serialises mutations; snapshots never include a password or token.
type Service struct {
	opMu          sync.Mutex
	mu            sync.Mutex
	vault         Vault
	newClient     func(context.Context) *protonmail.Client
	pending       *pendingLogin
	srv           *http.Server
	handler       *bridge.Handler
	cancel        context.CancelFunc
	address       string
	lastError     string
	keys          map[string]string
	accountErrors map[string]string
	history       map[string]bridge.AccountStatus
}

func New(vault Vault) *Service {
	return &Service{vault: vault, address: "127.0.0.1:8081", keys: make(map[string]string), accountErrors: make(map[string]string), history: make(map[string]bridge.AccountStatus),
		newClient: func(ctx context.Context) *protonmail.Client {
			return &protonmail.Client{Context: ctx, RootURL: "https://mail.proton.me/api", AppVersion: "Other", HTTPClient: &http.Client{Timeout: 45 * time.Second}}
		}}
}

func (s *Service) State() State {
	users, err := auth.ListUsernames()
	sort.Strings(users)
	s.mu.Lock()
	defer s.mu.Unlock()
	result := State{Running: s.srv != nil, Address: "http://" + s.address + "/caldav/", Stage: "account", Error: s.lastError, Accounts: []Account{}}
	if err != nil {
		result.Error = "Could not read stored accounts: " + err.Error()
	}
	if p := s.pending; p != nil && p.ctx.Err() == nil {
		result.Stage, result.PendingUsername = p.stage, p.username
	}
	for _, username := range users {
		entry := Account{Username: username, Locked: s.keys[username] == "", Error: s.accountErrors[username]}
		entry.AccountStatus = s.history[username]
		if s.handler != nil {
			entry.AccountStatus = preserveTimes(s.handler.Status(username), entry.AccountStatus)
			s.history[username] = entry.AccountStatus
		}
		result.Accounts = append(result.Accounts, entry)
	}
	return result
}

func preserveTimes(current, previous bridge.AccountStatus) bridge.AccountStatus {
	if current.LastSync.IsZero() {
		current.LastSync = previous.LastSync
	}
	if current.LastClient.IsZero() {
		current.LastClient = previous.LastClient
	}
	return current
}

// Start reserves localhost before loading credentials. A foreign listener is
// reported, never stopped or taken over.
func (s *Service) Start() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.srv != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		s.mu.Lock()
		s.lastError = "Port 8081 is already in use. Stop the other bridge before starting iOxide Desktop."
		s.mu.Unlock()
		return errors.New("cannot start the desktop bridge: localhost port 8081 is unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := bridge.NewHandler(ctx, auth.NewManager(func() *protonmail.Client { return s.newClient(ctx) }))
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	s.mu.Lock()
	s.srv, s.handler, s.cancel, s.lastError = srv, h, cancel, ""
	s.mu.Unlock()
	go func() { _ = srv.Serve(listener) }()
	go func() {
		users, err := auth.ListUsernames()
		if err != nil {
			return
		}
		for _, username := range users {
			// OS credential access can prompt the user; it must not hold the
			// operation lock or prevent the bridge listener from stopping.
			key, keyErr := s.vault.Get(username)
			s.opMu.Lock()
			s.mu.Lock()
			active := s.handler == h && ctx.Err() == nil
			if keyErr != nil {
				key = s.keys[username]
			}
			s.mu.Unlock()
			if !active {
				s.opMu.Unlock()
				return
			}
			if key == "" {
				s.setAccountError(username, "Enter your bridge password to unlock this account.")
				s.opMu.Unlock()
				continue
			}
			s.mu.Lock()
			s.keys[username] = key
			s.mu.Unlock()
			if err := h.Prepare(username, key); err != nil {
				s.setAccountError(username, "Could not resume the account. Check your connection or sign in again.")
			} else if keyErr != nil {
				s.setAccountError(username, "Unlocked for this session only. The operating system credential store is unavailable.")
			} else {
				s.mu.Lock()
				delete(s.accountErrors, username)
				s.mu.Unlock()
			}
			s.opMu.Unlock()
		}
	}()
	return nil
}

func (s *Service) setAccountError(username, message string) {
	s.mu.Lock()
	s.accountErrors[username] = message
	s.mu.Unlock()
}

func (s *Service) stop() {
	s.mu.Lock()
	srv, h, cancel := s.srv, s.handler, s.cancel
	if h != nil {
		for username := range s.keys {
			s.history[username] = preserveTimes(h.Status(username), s.history[username])
		}
	}
	s.srv, s.handler, s.cancel = nil, nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if srv != nil {
		ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
		_ = srv.Shutdown(ctx)
		done()
	}
	if h != nil {
		h.Close()
	}
}

func (s *Service) Stop() {
	s.CancelLogin()
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.stop()
}

func (s *Service) Close() {
	s.CancelLogin()
	s.Stop()
	s.mu.Lock()
	clear(s.keys)
	s.mu.Unlock()
}

func (s *Service) CancelLogin() {
	s.mu.Lock()
	if s.pending != nil {
		s.pending.cancel()
		s.pending = nil
	}
	s.mu.Unlock()
}

func (s *Service) BeginLogin(username, password string) (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return s.State(), errors.New("enter your username and password")
	}
	if len(username) > 320 || len(password) > 4096 {
		return s.State(), errors.New("account details are too long")
	}
	s.mu.Lock()
	running := s.srv != nil
	s.mu.Unlock()
	if !running {
		return s.State(), errors.New("start the local bridge before adding an account")
	}
	users, err := auth.ListUsernames()
	if err != nil {
		return s.State(), err
	}
	for _, user := range users {
		if strings.EqualFold(user, username) {
			return s.State(), errors.New("this account is already added; unlock it or remove it before signing in again")
		}
	}
	s.CancelLogin()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	p := &pendingLogin{ctx: ctx, cancel: cancel, username: username, password: password, client: s.newClient(ctx), stage: "signing-in"}
	s.mu.Lock()
	s.pending = p
	s.lastError = ""
	s.mu.Unlock()
	context.AfterFunc(ctx, func() {
		s.mu.Lock()
		if s.pending == p {
			s.pending = nil
		}
		s.mu.Unlock()
	})
	return s.attemptLogin(p)
}

func (s *Service) setStage(p *pendingLogin, stage string) {
	s.mu.Lock()
	if s.pending == p {
		p.stage = stage
	}
	s.mu.Unlock()
}

func (s *Service) attemptLogin(p *pendingLogin) (State, error) {
	info, err := p.client.AuthInfo(p.username)
	if err == nil {
		p.auth, err = p.client.Auth(p.username, p.password, info)
	}
	var apiErr *protonmail.APIError
	if errors.As(err, &apiErr) && apiErr.Code == protonmail.CodeHumanVerificationRequired && apiErr.HV != nil {
		s.mu.Lock()
		p.challenge = apiErr.HV
		s.mu.Unlock()
		s.setStage(p, "verification")
		return s.State(), nil
	}
	if err != nil {
		s.CancelLogin()
		return s.State(), errors.New("sign-in failed; check your account details and connection, then try again")
	}
	p.client.ClearHumanVerification()
	if p.auth.TwoFactor.Enabled != 0 {
		if p.auth.TwoFactor.TOTP != 1 {
			s.CancelLogin()
			return s.State(), errors.New("this account requires an unsupported second factor; use an authenticator code")
		}
		s.setStage(p, "two-factor")
		return s.State(), nil
	}
	return s.afterSecondFactor(p)
}

func (s *Service) pendingAt(stage string) (*pendingLogin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.ctx.Err() != nil || s.pending.stage != stage {
		return nil, errors.New("this sign-in step is no longer active; start sign-in again")
	}
	return s.pending, nil
}

func (s *Service) CompleteTOTP(code string) (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	p, err := s.pendingAt("two-factor")
	if err != nil {
		return s.State(), err
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return s.State(), errors.New("enter the six-digit code from your authenticator")
	}
	scope, err := p.client.AuthTOTP(code)
	if err != nil {
		return s.State(), errors.New("the code was not accepted; try a fresh code")
	}
	p.auth.Scope = scope
	return s.afterSecondFactor(p)
}

func (s *Service) afterSecondFactor(p *pendingLogin) (State, error) {
	if p.auth.PasswordMode == protonmail.PasswordTwo {
		s.setStage(p, "mailbox-password")
		return s.State(), nil
	}
	return s.finish(p, p.password)
}

func (s *Service) CompleteMailboxPassword(password string) (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	p, err := s.pendingAt("mailbox-password")
	if err != nil {
		return s.State(), err
	}
	if password == "" || len(password) > 4096 {
		return s.State(), errors.New("enter your separate mailbox password")
	}
	return s.finish(p, password)
}

func (s *Service) finish(p *pendingLogin, mailboxPassword string) (State, error) {
	if p.ctx.Err() != nil {
		return s.State(), errors.New("sign-in expired; start again")
	}
	salts, err := p.client.ListKeySalts()
	if err == nil {
		_, err = p.client.Unlock(p.auth, salts, mailboxPassword)
	}
	if err != nil {
		return s.State(), errors.New("could not unlock calendar keys; check the mailbox password and connection")
	}
	if p.ctx.Err() != nil {
		return s.State(), errors.New("sign-in expired; start again")
	}
	return s.commit(p, &auth.CachedAuth{Auth: *p.auth, LoginPassword: p.password, MailboxPassword: mailboxPassword, KeySalts: salts})
}

func (s *Service) commit(p *pendingLogin, cached *auth.CachedAuth) (State, error) {
	if p.ctx.Err() != nil {
		return s.State(), errors.New("sign-in expired; start again")
	}
	key, password, err := auth.GeneratePassword()
	if err != nil {
		return s.State(), err
	}
	previous, previousErr := s.vault.Get(p.username)
	rollback := func() {
		if previousErr == nil {
			_ = s.vault.Set(p.username, previous)
		} else {
			_ = s.vault.Delete(p.username)
		}
	}
	if err := s.vault.Set(p.username, password); err != nil {
		return s.State(), errors.New("could not save the bridge password in your operating system credential store; unlock the credential store and retry")
	}
	// Cancellation and credential commit share this short critical section.
	// Keychain prompts occur above it so cancellation does not wait on OS UI.
	s.mu.Lock()
	if s.pending != p || p.ctx.Err() != nil {
		s.mu.Unlock()
		rollback()
		return s.State(), errors.New("sign-in was cancelled; no account was saved")
	}
	if err := auth.CreateAndSave(cached, p.username, key); err != nil {
		s.mu.Unlock()
		rollback()
		return s.State(), err
	}
	s.keys[p.username] = password
	h := s.handler
	s.pending = nil
	p.cancel()
	s.mu.Unlock()
	if h != nil {
		if err := h.Prepare(p.username, password); err != nil {
			s.setAccountError(p.username, "Account saved. Could not start calendar loading; check the connection and restart the bridge.")
		}
	}
	return s.State(), nil
}

func (s *Service) UnlockAccount(username, password string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	h := s.handler
	s.mu.Unlock()
	if h == nil {
		return errors.New("start the bridge first")
	}
	if err := h.Prepare(username, password); err != nil {
		return errors.New("could not unlock this account; check the bridge password and connection")
	}
	s.mu.Lock()
	s.keys[username] = password
	delete(s.accountErrors, username)
	s.mu.Unlock()
	if err := s.vault.Set(username, password); err != nil {
		s.setAccountError(username, "Unlocked for this session only. Could not save the bridge password in the operating system credential store.")
		return errors.New("account unlocked for this session only; the operating system credential store could not save its bridge password")
	}
	return nil
}

// RevealPassword is separate from State so snapshots and background polls never
// send a bridge password to the webview.
func (s *Service) RevealPassword(username string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.keys[username]
	if key == "" {
		return "", errors.New("unlock this account before revealing its bridge password")
	}
	return key, nil
}

func (s *Service) RemoveAccount(username string) (resultErr error) {
	s.CancelLogin()
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.opMu.Lock()
	s.mu.Lock()
	wasRunning := s.srv != nil
	previousKey := s.keys[username]
	s.mu.Unlock()
	defer func() {
		s.opMu.Unlock()
		if wasRunning {
			resultErr = errors.Join(resultErr, s.Start())
		}
	}()
	s.stop()
	// Keep the account visible if keychain cleanup fails so removal can be retried.
	if err := s.vault.Delete(username); err != nil {
		return fmt.Errorf("account was not removed because its saved bridge password could not be deleted: %w", err)
	}
	if err := auth.Delete(username); err != nil {
		if previousKey != "" {
			_ = s.vault.Set(username, previousKey)
		}
		return err
	}
	s.mu.Lock()
	delete(s.keys, username)
	delete(s.accountErrors, username)
	delete(s.history, username)
	s.mu.Unlock()
	return nil
}
