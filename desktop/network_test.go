package main

import "testing"

func TestConnectionURLDoesNotAllowCredentialOrSchemeConfusion(t *testing.T) {
	for _, raw := range []string{"http://server/caldav/", "https://user:password@server/caldav/", "https://server/caldav/?token=secret", "https://server/caldav/#secret", "file:///etc/passwd", "https://server/admin", "https:///caldav/"} {
		if _, err := validateConnectionURL(raw); err == nil {
			t.Errorf("accepted unsafe or unrelated address %q", raw)
		}
	}
	for _, raw := range []string{"https://calendar.example", "https://calendar.example/caldav/", "https://calendar.example:8443/"} {
		value, err := validateConnectionURL(raw)
		if err != nil || value == "" {
			t.Errorf("valid address %q rejected: %v", raw, err)
		}
	}
}
