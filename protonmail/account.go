package protonmail

import "net/http"

// User contains the account keys needed to unlock its address keys.
type User struct {
	Keys []*PrivateKey
}

func (c *Client) GetCurrentUser() (*User, error) {
	req, err := c.newRequest(http.MethodGet, "/users", nil)
	if err != nil {
		return nil, err
	}

	var respData struct {
		resp
		User *User
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	return respData.User, nil
}

// Address contains the address keys used to decrypt and sign calendar data.
type Address struct {
	Email string
	Keys  []*PrivateKey
}

func (c *Client) ListAddresses() ([]*Address, error) {
	req, err := c.newRequest(http.MethodGet, "/addresses", nil)
	if err != nil {
		return nil, err
	}

	var respData struct {
		resp
		Addresses []*Address
	}
	if err := c.doJSON(req, &respData); err != nil {
		return nil, err
	}

	return respData.Addresses, nil
}
