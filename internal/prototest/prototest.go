// Package prototest serves a fake Proton account for tests: a user key and an
// address key, each encrypted under its own bcrypt-derived key password, plus
// the refresh endpoint. It never talks to Proton.
package prototest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/emersion/go-bcrypt"
)

// Account is a fake Proton account served over HTTP.
type Account struct {
	URL      string
	Password string
	// KeyPasswords are the derived key passwords by key ID, computed here
	// independently of the protonmail package.
	KeyPasswords map[string][]byte
	// RefreshInvalid makes /auth/refresh answer like an ended session.
	RefreshInvalid atomic.Bool
	// Refreshes counts successful refreshes.
	Refreshes atomic.Int32
	// UsersStatus, when set, is the HTTP status /users answers with, to
	// simulate an expired access token (401) or an outage (503).
	UsersStatus atomic.Int32
	// UnsaltedKey, when set, is a key ID served without a salt.
	UnsaltedKey atomic.Value
}

// KeyPassword derives a key password the way Proton does: bcrypt (cost 10)
// of the mailbox password with the key's salt, without the 29-byte prefix.
func KeyPassword(t testing.TB, password string, salt []byte) []byte {
	t.Helper()
	hashed, err := bcrypt.GenerateFromPasswordAndSalt([]byte(password), 10, salt)
	if err != nil {
		t.Fatal(err)
	}
	return hashed[29:]
}

func encryptedKey(t testing.TB, passphrase []byte) string {
	t.Helper()
	entity, err := openpgp.NewEntity("test", "", "test@example.com", &packet.Config{Algorithm: packet.PubKeyAlgoEdDSA})
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.EncryptPrivateKeys(passphrase, nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.SerializePrivateWithoutSigning(w, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// NewAccount starts a fake account whose mailbox password is password.
func NewAccount(t testing.TB, password string) *Account {
	t.Helper()
	salts := map[string][]byte{
		"user-key":    []byte("0123456789abcdef"),
		"address-key": []byte("fedcba9876543210"),
	}
	a := &Account{Password: password, KeyPasswords: map[string][]byte{}}
	keys := map[string]map[string]any{}
	for id, salt := range salts {
		a.KeyPasswords[id] = KeyPassword(t, password, salt)
		keys[id] = map[string]any{"ID": id, "Active": 1, "PrivateKey": encryptedKey(t, a.KeyPasswords[id])}
	}

	reply := func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
		if status := a.UsersStatus.Load(); status != 0 {
			w.WriteHeader(int(status))
			reply(w, map[string]any{"Code": status, "Error": "fake failure"})
			return
		}
		reply(w, map[string]any{"Code": 1000, "User": map[string]any{"Keys": []any{keys["user-key"]}}})
	})
	mux.HandleFunc("GET /addresses", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"Code": 1000, "Addresses": []any{map[string]any{"Email": "test@example.com", "Keys": []any{keys["address-key"]}}}})
	})
	mux.HandleFunc("GET /keys/salts", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]string
		unsalted, _ := a.UnsaltedKey.Load().(string)
		for id, salt := range salts {
			if id == unsalted {
				list = append(list, map[string]string{"ID": id, "KeySalt": ""})
				continue
			}
			list = append(list, map[string]string{"ID": id, "KeySalt": base64.StdEncoding.EncodeToString(salt)})
		}
		reply(w, map[string]any{"Code": 1000, "KeySalts": list})
	})
	// An account without calendars, so a calendar worker can list and settle.
	mux.HandleFunc("GET /calendar/v1", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"Code": 1000, "Calendars": []any{}})
	})
	mux.HandleFunc("POST /auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		if a.RefreshInvalid.Load() {
			w.WriteHeader(http.StatusUnprocessableEntity)
			reply(w, map[string]any{"Code": 10013, "Error": "Invalid refresh token"})
			return
		}
		a.Refreshes.Add(1)
		reply(w, map[string]any{"Code": 1000, "UID": "uid", "AccessToken": "access-2", "RefreshToken": "refresh-2", "ExpiresIn": 3600})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a.URL = srv.URL
	return a
}
