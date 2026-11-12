package protonmail

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
)

type PrivateKey struct {
	ID          string
	PrivateKey  string
	Fingerprint string
	Active      int
	Token       string
	Signature   string
}

func (priv *PrivateKey) Entity() (*openpgp.Entity, error) {
	keyRing, err := openpgp.ReadArmoredKeyRing(strings.NewReader(priv.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %v", err)
	}
	if len(keyRing) == 0 {
		return nil, errors.New("private key is empty")
	}
	return keyRing[0], nil
}

type PublicKeyResp struct {
	Keys []*PublicKey
}

type PublicKey struct {
	PublicKey string
}

func (pub *PublicKey) Entity() (*openpgp.Entity, error) {
	keyRing, err := openpgp.ReadArmoredKeyRing(strings.NewReader(pub.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("failed to read public key: %v", err)
	}
	if len(keyRing) == 0 {
		return nil, errors.New("public key is empty")
	}
	return keyRing[0], nil
}

// GetPublicKeys retrieves public keys for a user.
func (c *Client) GetPublicKeys(email string) (*PublicKeyResp, error) {
	v := url.Values{}
	v.Set("Email", email)
	// TODO: Fingerprint

	req, err := c.newRequest(http.MethodGet, "/keys?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var respData struct {
		resp
		*PublicKeyResp
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	return respData.PublicKeyResp, nil
}
