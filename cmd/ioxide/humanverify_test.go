package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mrk1987/ioxide/internal/protonmail"
)

// fakeVerificationAPI demands verification until the challenge counts as
// solved, then answers with an ordinary error so login stops before SRP.
func fakeVerificationAPI(t *testing.T, solveAfter int) (*protonmail.Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var sent []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		token := r.Header.Get("X-Pm-Human-Verification-Token")
		sent = append(sent, token+"|"+r.Header.Get("X-Pm-Human-Verification-Token-Type"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		if token != "" && len(sent) > solveAfter {
			_, _ = io.WriteString(w, `{"Code":8002,"Error":"stop here"}`)
			return
		}
		_, _ = io.WriteString(w, `{"Code":9001,"Error":"verify","Details":{"HumanVerificationMethods":["captcha","email"],"HumanVerificationToken":"challenge token/1"}}`)
	}))
	t.Cleanup(api.Close)
	return &protonmail.Client{Context: context.Background(), RootURL: api.URL, AppVersion: "Other", HTTPClient: api.Client()}, &sent
}

func TestLoginRetriesWithChallengeTokenAfterVerification(t *testing.T) {
	c, sent := fakeVerificationAPI(t, 1)
	var pages []string
	previous := waitForVerification
	waitForVerification = func(page string) error { pages = append(pages, page); return nil }
	t.Cleanup(func() { waitForVerification = previous })

	_, err := login(c, "sample", "dummy")
	if err == nil || !strings.Contains(err.Error(), "stop here") {
		t.Fatalf("login did not continue past verification: %v", err)
	}
	if len(pages) != 1 || pages[0] != "https://verify.proton.me/?methods=captcha%2Cemail&token=challenge+token%2F1" {
		t.Fatalf("verification pages: %v", pages)
	}
	if got := *sent; len(got) != 2 || got[0] != "|" || got[1] != "challenge token/1|captcha,email" {
		t.Fatalf("requests: %v", got)
	}
}

func TestLoginStopsWhenVerificationIsNeverAccepted(t *testing.T) {
	c, _ := fakeVerificationAPI(t, 1000)
	rounds := 0
	previous := waitForVerification
	waitForVerification = func(string) error { rounds++; return nil }
	t.Cleanup(func() { waitForVerification = previous })

	_, err := login(c, "sample", "dummy")
	if err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("unexpected result: %v", err)
	}
	if rounds != maxVerificationRounds {
		t.Fatalf("asked %d times, want %d", rounds, maxVerificationRounds)
	}
}
