package protonmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

type authInfoReq struct {
	Username string
}

type AuthInfo struct {
	version         int
	modulus         string
	serverEphemeral string
	salt            string
	srpSession      string
}

type AuthInfoResp struct {
	resp
	AuthInfo
	Version         int
	Modulus         string
	ServerEphemeral string
	Salt            string
	SRPSession      string
}

func (resp *AuthInfoResp) authInfo() *AuthInfo {
	info := &resp.AuthInfo
	info.version = resp.Version
	info.modulus = resp.Modulus
	info.serverEphemeral = resp.ServerEphemeral
	info.salt = resp.Salt
	info.srpSession = resp.SRPSession
	return info
}

func (c *Client) AuthInfo(username string) (*AuthInfo, error) {
	reqData := &authInfoReq{
		Username: username,
	}

	req, err := c.newJSONRequest(http.MethodPost, "/auth/info", reqData)
	if err != nil {
		return nil, err
	}

	var respData AuthInfoResp
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	return respData.authInfo(), nil
}

type authReq struct {
	Username        string
	SRPSession      string
	ClientEphemeral string
	ClientProof     string
}

type PasswordMode int

const (
	PasswordSingle PasswordMode = 1
	PasswordTwo    PasswordMode = 2
)

type Auth struct {
	ExpiresAt    time.Time
	Scope        string
	UID          string
	AccessToken  string
	RefreshToken string
	UserID       string
	EventID      string
	PasswordMode PasswordMode
	TwoFactor    struct {
		Enabled int
		U2F     interface{} // TODO
		TOTP    int
	} `json:"2FA"`
}

type authResp struct {
	resp
	Auth
	ExpiresIn   int
	TokenType   string
	ServerProof string
}

func (resp *authResp) auth() *Auth {
	auth := &resp.Auth
	auth.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	return auth
}

func (c *Client) Auth(username, password string, info *AuthInfo) (*Auth, error) {
	if info == nil {
		var err error
		if info, err = c.AuthInfo(username); err != nil {
			return nil, err
		}
	}

	proofs, err := srp([]byte(password), info)
	if err != nil {
		return nil, fmt.Errorf("SRP failed during auth: %v", err)
	}

	reqData := &authReq{
		Username:        username,
		SRPSession:      info.srpSession,
		ClientEphemeral: base64.StdEncoding.EncodeToString(proofs.clientEphemeral),
		ClientProof:     base64.StdEncoding.EncodeToString(proofs.clientProof),
	}

	req, err := c.newJSONRequest(http.MethodPost, "/auth", reqData)
	if err != nil {
		return nil, err
	}

	var respData authResp
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	if err := proofs.VerifyServerProof(respData.ServerProof); err != nil {
		return nil, err
	}

	auth := respData.auth()
	c.setSession(auth.UID, auth.AccessToken)
	return auth, nil
}

func (c *Client) AuthTOTP(code string) (scope string, err error) {
	reqData := struct {
		TwoFactorCode string
	}{
		TwoFactorCode: code,
	}

	req, err := c.newJSONRequest(http.MethodPost, "/auth/2fa", reqData)
	if err != nil {
		return "", err
	}

	respData := struct {
		resp
		Scope string
	}{}
	if err := c.doJSON(req, &respData); err != nil {
		return "", err
	}

	return respData.Scope, nil
}

type authRefreshReq struct {
	RefreshToken string

	// Unused but required
	ResponseType string
	GrantType    string
	RedirectURI  string
}

func (c *Client) AuthRefresh(expiredAuth *Auth) (*Auth, error) {
	reqData := &authRefreshReq{
		RefreshToken: expiredAuth.RefreshToken,
		ResponseType: "token",
		GrantType:    "refresh_token",
		RedirectURI:  "http://www.protonmail.ch",
	}

	req, err := c.newJSONRequest(http.MethodPost, "/auth/refresh", reqData)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Pm-Uid", expiredAuth.UID)

	var respData authResp
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	auth := respData.auth()
	auth.PasswordMode = expiredAuth.PasswordMode
	return auth, nil
}

func (c *Client) ListKeySalts() (map[string][]byte, error) {
	req, err := c.newRequest(http.MethodGet, "/keys/salts", nil)
	if err != nil {
		return nil, err
	}

	var respData struct {
		resp
		KeySalts []struct {
			ID      string
			KeySalt string
		}
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	salts := make(map[string][]byte, len(respData.KeySalts))
	for _, salt := range respData.KeySalts {
		if salt.KeySalt == "" {
			salts[salt.ID] = nil
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(salt.KeySalt)
		if err != nil {
			return nil, fmt.Errorf("failed to decode key salt payload: %v", err)
		}
		salts[salt.ID] = payload
	}

	return salts, nil
}

func unlockEntity(e *openpgp.Entity, passphraseBytes []byte) error {
	var privateKeys []*packet.PrivateKey

	// e.PrivateKey is a signing key
	if e.PrivateKey != nil {
		privateKeys = append(privateKeys, e.PrivateKey)
	}

	// e.Subkeys are encryption keys
	for _, subkey := range e.Subkeys {
		if subkey.PrivateKey != nil {
			privateKeys = append(privateKeys, subkey.PrivateKey)
		}
	}

	for _, priv := range privateKeys {
		if err := priv.Decrypt(passphraseBytes); err != nil {
			return err
		}
	}

	return nil
}

func decryptPrivateKeyToken(key *PrivateKey, userKeyRing openpgp.EntityList) ([]byte, error) {
	block, err := armor.Decode(strings.NewReader(key.Token))
	if err != nil {
		return nil, err
	}

	md, err := openpgp.ReadMessage(block.Body, userKeyRing, nil, nil)
	if err != nil {
		return nil, err
	}

	b, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, err
	}

	// TODO: check signer?
	_, err = openpgp.CheckArmoredDetachedSignature(userKeyRing, bytes.NewReader(b), strings.NewReader(key.Signature), nil)
	return b, err
}

// KeyPassphrases maps a key ID to the passphrase that unlocks that key: the
// bcrypt-derived key password, never the mailbox password itself. Keys
// without a salt are refused (ErrUnsaltedKey) rather than stored.
type KeyPassphrases map[string][]byte

// keyPassphrase returns the passphrase for a key that has no token.
type keyPassphrase func(key *PrivateKey) ([]byte, error)

// ErrUnsaltedKey means a key is encrypted with the mailbox password itself.
// Its passphrase would be the password, so iOxide refuses to store it.
var ErrUnsaltedKey = errors.New("this key has no salt and would require storing the mailbox password; change the password in Proton to upgrade it")

func passwordPassphrase(keySalts map[string][]byte, password []byte) keyPassphrase {
	return func(key *PrivateKey) ([]byte, error) {
		if salt := keySalts[key.ID]; salt != nil {
			return computeKeyPassword(password, salt)
		}
		return nil, ErrUnsaltedKey
	}
}

// ErrSessionExpired means the stored session can no longer be renewed, for
// example after a password change or when the session was revoked. Only
// signing in again fixes it; iOxide does not keep the password to do so.
var ErrSessionExpired = errors.New("the Proton session has ended; sign in again")

// ErrMissingKeyPassphrase means a key appeared that the stored passphrases
// cannot unlock, for example after a password change; signing in again fixes it.
var ErrMissingKeyPassphrase = errors.New("no stored passphrase for this key")

func storedPassphrase(passphrases KeyPassphrases) keyPassphrase {
	return func(key *PrivateKey) ([]byte, error) {
		passphrase, ok := passphrases[key.ID]
		if !ok {
			return nil, ErrMissingKeyPassphrase
		}
		return passphrase, nil
	}
}

func unlockPrivateKey(key *PrivateKey, userKeyRing openpgp.EntityList, passphraseFor keyPassphrase, used KeyPassphrases) (*openpgp.Entity, error) {
	entity, err := key.Entity()
	if err != nil {
		return nil, err
	}

	var passphraseBytes []byte
	token := key.Token != ""
	if token {
		passphraseBytes, err = decryptPrivateKeyToken(key, userKeyRing)
	} else {
		passphraseBytes, err = passphraseFor(key)
	}
	if err != nil {
		return nil, err
	}

	if err := unlockEntity(entity, passphraseBytes); err != nil {
		return nil, err
	}
	// Token keys are unlocked through the user key and need nothing stored.
	if !token && used != nil {
		used[key.ID] = passphraseBytes
	}

	return entity, nil
}

func unlockKeyRing(keys []*PrivateKey, userKeyRing openpgp.EntityList, passphraseFor keyPassphrase, used KeyPassphrases) (openpgp.EntityList, error) {
	var keyRing openpgp.EntityList
	var cause error
	for _, key := range keys {
		if key.Active != 1 {
			continue
		}

		entity, err := unlockPrivateKey(key, userKeyRing, passphraseFor, used)
		if err != nil {
			cause = keepCause(cause, err)
			log.Printf("warning: failed to unlock key %v: %v", key.Fingerprint, err)
			continue
		}

		keyRing = append(keyRing, entity)
	}

	if len(keyRing) == 0 {
		return nil, noKeysUnlocked(cause)
	}
	return keyRing, nil
}

// keepCause remembers the first failure that a caller can act on: a stored
// record that no longer fits the account, or a key iOxide refuses to store.
func keepCause(cause, err error) error {
	if cause != nil {
		return cause
	}
	for _, sentinel := range []error{ErrMissingKeyPassphrase, ErrUnsaltedKey} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return nil
}

// noKeysUnlocked keeps that cause visible through errors.Is.
func noKeysUnlocked(cause error) error {
	if cause != nil {
		return fmt.Errorf("failed to unlock any key: %w", cause)
	}
	return errors.New("failed to unlock any key")
}

// Unlock decrypts the account's private keys with the mailbox password and
// makes the client use auth's session.
func (c *Client) Unlock(auth *Auth, keySalts map[string][]byte, passphrase string) (openpgp.EntityList, error) {
	keyRing, _, err := c.UnlockWithPassword(auth, keySalts, passphrase)
	return keyRing, err
}

// UnlockWithPassword is Unlock that also returns the passphrase that unlocked
// each key, so the keys can be unlocked again without the mailbox password.
func (c *Client) UnlockWithPassword(auth *Auth, keySalts map[string][]byte, password string) (openpgp.EntityList, KeyPassphrases, error) {
	used := KeyPassphrases{}
	keyRing, err := c.unlock(auth, passwordPassphrase(keySalts, []byte(password)), used)
	if err != nil {
		return nil, nil, err
	}
	return keyRing, used, nil
}

// UnlockWithKeyPassphrases unlocks the account's keys with passphrases
// returned by UnlockWithPassword.
func (c *Client) UnlockWithKeyPassphrases(auth *Auth, passphrases KeyPassphrases) (openpgp.EntityList, error) {
	return c.unlock(auth, storedPassphrase(passphrases), nil)
}

func (c *Client) unlock(auth *Auth, passphraseFor keyPassphrase, used KeyPassphrases) (openpgp.EntityList, error) {
	c.setSession(auth.UID, auth.AccessToken)

	u, err := c.GetCurrentUser()
	if err != nil {
		return nil, err
	}

	userKeyRing, err := unlockKeyRing(u.Keys, nil, passphraseFor, used)
	if err != nil {
		return nil, err
	}

	addrs, err := c.ListAddresses()
	if err != nil {
		return nil, err
	}

	var keyRing openpgp.EntityList
	var cause error
	for _, addr := range addrs {
		addrKeyRing, err := unlockKeyRing(addr.Keys, userKeyRing, passphraseFor, used)
		if err != nil {
			cause = keepCause(cause, err)
			log.Printf("warning: failed to unlock address <%v>: %v", addr.Email, err)
			continue
		}

		keyRing = append(keyRing, addrKeyRing...)
	}

	if len(keyRing) == 0 {
		return nil, noKeysUnlocked(cause)
	}

	return keyRing, nil
}
