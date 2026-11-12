package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/mrk1987/ioxide/internal/auth"
	"github.com/mrk1987/ioxide/internal/protonmail"
)

func TestServerRequiresCredentials(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", t.TempDir())
	case "darwin":
		t.Setenv("HOME", t.TempDir())
	default:
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}

	srv := newServer(auth.NewManager(func() *protonmail.Client { return &protonmail.Client{} }))

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/caldav/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no credentials: status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("WWW-Authenticate = %q", got)
	}

	req := httptest.NewRequest("PROPFIND", "/caldav/", nil)
	req.SetBasicAuth("nobody", "wrong")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong credentials: status = %d, want 401", rec.Code)
	}
}
