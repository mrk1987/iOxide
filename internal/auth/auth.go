// Package auth stores Proton credentials encrypted under a bridge password and
// authenticates CalDAV clients that present it.
//
// The bridge password is a random 256-bit key, shown once at login. Clients
// send it as their HTTP Basic password. It never reaches Proton: it only
// unlocks the locally stored session, which in turn holds what Proton needs.
package auth

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	"golang.org/x/crypto/nacl/secretbox"

	"github.com/mrk1987/ioxide/internal/protonmail"
)

const (
	keySize   = 32
	nonceSize = 24
)

// ErrUnauthorized is returned when the username or bridge password is wrong.
var ErrUnauthorized = errors.New("invalid username or password")

// ErrSessionExpired is protonmail.ErrSessionExpired, re-exported for callers
// that only deal with stored credentials.
var ErrSessionExpired = protonmail.ErrSessionExpired

// CachedAuth is everything needed to resume a Proton session without the
// user: the session tokens and the passphrases that unlock the account's keys.
// Like Proton Bridge, it holds neither the login nor the mailbox password.
type CachedAuth struct {
	protonmail.Auth
	KeyPassphrases protonmail.KeyPassphrases `json:",omitempty"`

	// Records written before KeyPassphrases held the passwords themselves.
	// They are read once, replaced by KeyPassphrases and never written again.
	LoginPassword   string            `json:",omitempty"`
	MailboxPassword string            `json:",omitempty"`
	KeySalts        map[string][]byte `json:",omitempty"`
}

// NewCachedAuth unlocks the account's keys with the mailbox password to prove
// it is right, and returns a record that keeps only what resuming needs.
func NewCachedAuth(c *protonmail.Client, a *protonmail.Auth, mailboxPassword string) (*CachedAuth, error) {
	keySalts, err := c.ListKeySalts()
	if err != nil {
		return nil, fmt.Errorf("fetching key salts: %w", err)
	}
	_, passphrases, err := c.UnlockWithPassword(a, keySalts, mailboxPassword)
	if err != nil {
		return nil, err
	}
	return &CachedAuth{Auth: *a, KeyPassphrases: passphrases}, nil
}

// legacy reports whether the record still holds passwords.
func (cached *CachedAuth) legacy() bool {
	return len(cached.KeyPassphrases) == 0
}

// forgetPasswords keeps only the derived key passphrases.
func (cached *CachedAuth) forgetPasswords(passphrases protonmail.KeyPassphrases) {
	cached.KeyPassphrases = passphrases
	cached.LoginPassword, cached.MailboxPassword, cached.KeySalts = "", "", nil
}

// GeneratePassword returns a fresh secret key and its bridge-password form.
func GeneratePassword() (secretKey *[keySize]byte, password string, err error) {
	var key [keySize]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return nil, "", fmt.Errorf("generating key: %w", err)
	}
	return &key, base64.StdEncoding.EncodeToString(key[:]), nil
}

// KeyFromPassword returns the secret key behind an existing bridge password,
// so an account can be signed in again without changing it.
func KeyFromPassword(password string) (*[keySize]byte, error) {
	return parsePassword(password)
}

func parsePassword(password string) (*[keySize]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(password)
	if err != nil || len(raw) != keySize {
		return nil, ErrUnauthorized
	}
	var key [keySize]byte
	copy(key[:], raw)
	return &key, nil
}

func encrypt(msg []byte, secretKey *[keySize]byte) (string, error) {
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	sealed := secretbox.Seal(nonce[:], msg, &nonce, secretKey)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decrypt(encoded string, secretKey *[keySize]byte) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid encrypted data: %w", err)
	}
	if len(sealed) < nonceSize {
		return nil, errors.New("invalid encrypted data: too short")
	}
	var nonce [nonceSize]byte
	copy(nonce[:], sealed[:nonceSize])
	msg, ok := secretbox.Open(nil, sealed[nonceSize:], &nonce, secretKey)
	if !ok {
		return nil, errors.New("decryption failed")
	}
	return msg, nil
}

// EncryptAndSave stores the credentials for username, encrypted under
// secretKey, replacing any existing entry.
func EncryptAndSave(cached *CachedAuth, username string, secretKey *[keySize]byte) error {
	return encryptAndSave(cached, username, secretKey, false)
}

// CreateAndSave refuses to replace an account added by another process while
// desktop sign-in was in progress.
func CreateAndSave(cached *CachedAuth, username string, secretKey *[keySize]byte) error {
	return encryptAndSave(cached, username, secretKey, true)
}

// Replace stores a new sign-in for an existing account under its existing
// bridge password, so calendar apps keep working. It refuses when that
// password does not open the stored entry, which means another process
// replaced the account in the meantime.
func Replace(cached *CachedAuth, username string, secretKey *[keySize]byte) error {
	unlock, err := lockStore()
	if err != nil {
		return err
	}
	defer unlock()
	store, err := loadStore()
	if err != nil {
		return err
	}
	encrypted, ok := store[username]
	if !ok {
		return errors.New("this account was removed by another iOxide process; add it again")
	}
	cleartext, err := decrypt(encrypted, secretKey)
	if err != nil {
		return errors.New("this account was changed by another iOxide process; unlock it with its new bridge password")
	}
	// Calendar apps keep using this bridge password, so it must stay with
	// the same Proton account.
	var previous CachedAuth
	if err := json.Unmarshal(cleartext, &previous); err != nil {
		return fmt.Errorf("decoding stored credentials: %w", err)
	}
	if previous.UserID != "" && cached.UserID != "" && previous.UserID != cached.UserID {
		return errors.New("this sign-in is for a different Proton account; remove the account and add it again instead")
	}
	updated, err := json.Marshal(cached)
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}
	if store[username], err = encrypt(updated, secretKey); err != nil {
		return err
	}
	return saveStore(store)
}

func encryptAndSave(cached *CachedAuth, username string, secretKey *[keySize]byte, createOnly bool) error {
	unlock, err := lockStore()
	if err != nil {
		return err
	}
	defer unlock()
	cleartext, err := json.Marshal(cached)
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}
	encrypted, err := encrypt(cleartext, secretKey)
	if err != nil {
		return err
	}

	store, err := loadStore()
	if err != nil {
		return err
	}
	if _, exists := store[username]; createOnly && exists {
		return errors.New("this account was added by another iOxide process; unlock the existing account instead")
	}
	store[username] = encrypted
	return saveStore(store)
}

// ListUsernames returns the usernames with stored credentials.
func ListUsernames() ([]string, error) {
	store, err := loadStore()
	if err != nil {
		return nil, err
	}
	usernames := make([]string, 0, len(store))
	for username := range store {
		usernames = append(usernames, username)
	}
	return usernames, nil
}

// authenticate refreshes the stored session, falling back to a full login
// with the stored password when the refresh token is no longer valid, and
// unlocks the account's keys. It updates cached with the new session.
//
// save is called as soon as the session tokens change. Proton invalidates the
// old refresh token on every refresh, and iOxide keeps no password to recover,
// so the new tokens must reach disk before anything else can fail.
func authenticate(c *protonmail.Client, cached *CachedAuth, username string, save func() error) (openpgp.EntityList, error) {
	if cached.RefreshToken == "" {
		// An imported session may have no refresh token. Use its access
		// token as long as it lasts.
		return unlockKeys(c, cached)
	}

	auth, err := c.AuthRefresh(&cached.Auth)
	var apiErr *protonmail.APIError
	if errors.As(err, &apiErr) && apiErr.Code == protonmail.CodeInvalidRefreshToken {
		if cached.LoginPassword == "" {
			return nil, ErrSessionExpired
		}
		// A record from an older build: use its password this one last time.
		authInfo, err := c.AuthInfo(username)
		if err != nil {
			return nil, fmt.Errorf("re-authenticating: %w", err)
		}
		auth, err = c.Auth(username, cached.LoginPassword, authInfo)
		if err != nil {
			return nil, fmt.Errorf("re-authenticating: %w", err)
		}
		if auth.TwoFactor.Enabled != 0 {
			return nil, fmt.Errorf("re-authenticating needs a two-factor code: %w", ErrSessionExpired)
		}
	} else if err != nil {
		return nil, err
	}
	cached.Auth = *auth
	if err := save(); err != nil {
		return nil, fmt.Errorf("saving the renewed session: %w", err)
	}

	return unlockKeys(c, cached)
}

// unlockKeys unlocks with the stored key passphrases. A record from an older
// build is unlocked with its mailbox password once and then rewritten without
// any password; the caller saves the record after every authenticate.
func unlockKeys(c *protonmail.Client, cached *CachedAuth) (openpgp.EntityList, error) {
	if !cached.legacy() {
		keys, err := c.UnlockWithKeyPassphrases(&cached.Auth, cached.KeyPassphrases)
		if errors.Is(err, protonmail.ErrMissingKeyPassphrase) {
			return nil, ErrSessionExpired
		}
		return keys, err
	}
	keys, passphrases, err := c.UnlockWithPassword(&cached.Auth, cached.KeySalts, cached.MailboxPassword)
	if err != nil {
		return nil, err
	}
	cached.forgetPasswords(passphrases)
	return keys, nil
}

// Session is a logged-in Proton session for one user.
type Session struct {
	Client      *protonmail.Client
	PrivateKeys openpgp.EntityList
	// CacheKey protects data the server stores on disk for this user. It is
	// derived from the bridge password, so it is never stored anywhere.
	CacheKey [keySize]byte

	keyDigest [sha256.Size]byte
}

func deriveCacheKey(secretKey *[keySize]byte) ([keySize]byte, error) {
	var key [keySize]byte
	derived, err := hkdf.Key(sha256.New, secretKey[:], nil, "ioxide calendar cache v1", keySize)
	if err != nil {
		return key, fmt.Errorf("deriving cache key: %w", err)
	}
	copy(key[:], derived)
	return key, nil
}

// Manager authenticates users by bridge password and keeps one logged-in
// Proton client per user.
type Manager struct {
	newClient func() *protonmail.Client

	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager returns a Manager that creates Proton clients with newClient.
func NewManager(newClient func() *protonmail.Client) *Manager {
	return &Manager{
		newClient: newClient,
		sessions:  map[string]*Session{},
	}
}

// Auth checks the bridge password for username and returns the user's
// session. The first call for a user resumes the stored Proton session;
// later calls reuse it.
func (m *Manager) Auth(username, password string) (*Session, error) {
	secretKey, err := parsePassword(password)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(secretKey[:])

	// Opening a session rotates the refresh token, so two first requests
	// arriving together must not both do it. Holding the lock throughout
	// serialises them; the common case, a cached session, is quick.
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[username]; ok && subtle.ConstantTimeCompare(s.keyDigest[:], digest[:]) == 1 {
		return s, nil
	}

	// No session yet, or the bridge password changed because `ioxide auth`
	// was run again while serving. Either way the stored credentials decide.
	s, err := m.openSession(username, secretKey)
	if err != nil {
		return nil, err
	}
	s.keyDigest = digest
	if s.CacheKey, err = deriveCacheKey(secretKey); err != nil {
		return nil, err
	}
	m.sessions[username] = s
	return s, nil
}

// Forget drops the open session for username, so the next request reads the
// stored credentials again, for example after the account was signed in again.
func (m *Manager) Forget(username string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, username)
}

func (m *Manager) openSession(username string, secretKey *[keySize]byte) (*Session, error) {
	store, err := loadStore()
	if err != nil {
		return nil, err
	}
	encrypted, ok := store[username]
	if !ok {
		return nil, ErrUnauthorized
	}
	decrypted, err := decrypt(encrypted, secretKey)
	if err != nil {
		return nil, ErrUnauthorized
	}
	var cached CachedAuth
	if err := json.Unmarshal(decrypted, &cached); err != nil {
		return nil, fmt.Errorf("decoding stored credentials: %w", err)
	}

	c := m.newClient()
	save := func() error { return EncryptAndSave(&cached, username, secretKey) }
	c.ReAuth = func() error {
		// An access-token-only session (imported without a refresh token)
		// cannot be renewed. Retrying it would resend the expired token
		// from inside the renewal and wait on itself.
		if cached.RefreshToken == "" {
			return ErrSessionExpired
		}
		if _, err := authenticate(c, &cached, username, save); err != nil {
			return err
		}
		return save()
	}

	privateKeys, err := authenticate(c, &cached, username, save)
	if err != nil {
		return nil, err
	}
	// Persist the rotated tokens and, for a record from an older build, the
	// version without passwords.
	if err := save(); err != nil {
		return nil, err
	}

	return &Session{Client: c, PrivateKeys: privateKeys}, nil
}
