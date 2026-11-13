package desktop

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

// The fake API demands verification until the challenge is marked solved, then
// answers with an ordinary error so the test can stop before the SRP exchange.
func TestVerificationOpensProtonPageAndRetriesWithChallengeToken(t *testing.T) {
	s, _ := testService(t)
	var mu sync.Mutex
	solved := false
	var sentToken, sentType string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		sentToken = r.Header.Get("X-Pm-Human-Verification-Token")
		sentType = r.Header.Get("X-Pm-Human-Verification-Token-Type")
		w.Header().Set("Content-Type", "application/json")
		if solved && sentToken == "challenge token/1" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"Code":8002,"Error":"stop here"}`)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"Code":9001,"Error":"verify","Details":{"HumanVerificationMethods":["captcha","email"],"HumanVerificationToken":"challenge token/1"}}`)
	}))
	defer api.Close()
	s.newClient = func(ctx context.Context) *protonmail.Client {
		return &protonmail.Client{Context: ctx, RootURL: api.URL, AppVersion: "Other", HTTPClient: api.Client()}
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}

	state, err := s.BeginLogin("sample", "dummy")
	if err != nil || state.Stage != "verification" {
		t.Fatalf("stage %q, err %v", state.Stage, err)
	}
	if sentToken != "" {
		t.Fatal("first sign-in attempt sent a verification token")
	}
	page, err := s.VerificationURL()
	if err != nil {
		t.Fatal(err)
	}
	if page != "https://verify.proton.me/?methods=captcha%2Cemail&token=challenge+token%2F1" {
		t.Fatalf("verification page: %s", page)
	}

	// Continuing before the challenge is solved keeps the step open.
	state, err = s.ContinueVerification()
	if err == nil || state.Stage != "verification" {
		t.Fatalf("unsolved challenge: stage %q, err %v", state.Stage, err)
	}
	if !strings.Contains(err.Error(), "not complete") {
		t.Fatalf("unexpected message: %v", err)
	}

	mu.Lock()
	solved = true
	mu.Unlock()
	state, err = s.ContinueVerification()
	if sentToken != "challenge token/1" || sentType != "captcha,email" {
		t.Fatalf("retry sent token %q type %q", sentToken, sentType)
	}
	// The fake API stops sign-in after verification; the step must be closed.
	if err == nil || state.Stage != "account" {
		t.Fatalf("after solved challenge: stage %q, err %v", state.Stage, err)
	}
}
