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

// CachedAuth is everything needed to resume a Proton session without the
// user: the session tokens, the passwords to re-login when they expire, and
// the key salts to unlock the account's keys.
type CachedAuth struct {
	protonmail.Auth
	LoginPassword   string
	MailboxPassword string
	KeySalts        map[string][]byte
}

// GeneratePassword returns a fresh secret key and its bridge-password form.
func GeneratePassword() (secretKey *[keySize]byte, password string, err error) {
	var key [keySize]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return nil, "", fmt.Errorf("generating key: %w", err)
	}
	return &key, base64.StdEncoding.EncodeToString(key[:]), nil
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
func authenticate(c *protonmail.Client, cached *CachedAuth, username string) (openpgp.EntityList, error) {
	if cached.RefreshToken == "" {
		// An imported session may have no refresh token. Use its access
		// token as long as it lasts.
		return c.Unlock(&cached.Auth, cached.KeySalts, cached.MailboxPassword)
	}

	auth, err := c.AuthRefresh(&cached.Auth)
	var apiErr *protonmail.APIError
	if errors.As(err, &apiErr) && apiErr.Code == protonmail.CodeInvalidRefreshToken {
		authInfo, err := c.AuthInfo(username)
		if err != nil {
			return nil, fmt.Errorf("re-authenticating: %w", err)
		}
		auth, err = c.Auth(username, cached.LoginPassword, authInfo)
		if err != nil {
			return nil, fmt.Errorf("re-authenticating: %w", err)
		}
		if auth.TwoFactor.Enabled != 0 {
			return nil, errors.New("re-authenticating: two-factor authentication is enabled, log in again with `ioxide auth`")
		}
	} else if err != nil {
		return nil, err
	}
	cached.Auth = *auth

	return c.Unlock(auth, cached.KeySalts, cached.MailboxPassword)
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
	c.ReAuth = func() error {
		if _, err := authenticate(c, &cached, username); err != nil {
			return err
		}
		return EncryptAndSave(&cached, username, secretKey)
	}

	privateKeys, err := authenticate(c, &cached, username)
	if err != nil {
		return nil, err
	}
	// authenticate rotated the refresh token; persist it.
	if err := EncryptAndSave(&cached, username, secretKey); err != nil {
		return nil, err
	}

	return &Session{Client: c, PrivateKeys: privateKeys}, nil
}
