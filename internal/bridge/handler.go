// Package bridge authenticates CalDAV clients and owns their cache workers.
package bridge

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/mrk1987/ioxide/internal/auth"
	"github.com/mrk1987/ioxide/internal/caldav"
)

type accountHandler struct {
	session    *auth.Session
	handler    *caldav.Handler
	lastClient time.Time
}

type Handler struct {
	ctx      context.Context
	manager  *auth.Manager
	mu       sync.Mutex
	accounts map[string]*accountHandler
	closed   bool
}

func NewHandler(ctx context.Context, manager *auth.Manager) *Handler {
	return &Handler{ctx: ctx, manager: manager, accounts: make(map[string]*accountHandler)}
}

func (h *Handler) Prepare(username, password string) error {
	_, err := h.account(username, password)
	return err
}

func (h *Handler) account(username, password string) (*accountHandler, error) {
	if h.ctx.Err() != nil {
		return nil, h.ctx.Err()
	}
	session, err := h.manager.Auth(username, password)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("bridge stopped")
	}
	if previous, ok := h.accounts[username]; ok && previous.session == session {
		return previous, nil
	}
	if previous := h.accounts[username]; previous != nil {
		previous.handler.Close()
	}
	calendarHandler, err := caldav.NewHandlerContext(h.ctx, session.Client, session.PrivateKeys, username, &session.CacheKey)
	if err != nil {
		return nil, err
	}
	entry := &accountHandler{session: session, handler: calendarHandler}
	h.accounts[username] = entry
	return entry, nil
}

type AccountStatus struct {
	caldav.CacheStatus
	LastClient time.Time `json:"lastClient"`
}

func (h *Handler) Status(username string) AccountStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.accounts[username]
	if entry == nil {
		return AccountStatus{}
	}
	return AccountStatus{CacheStatus: entry.handler.Status(), LastClient: entry.lastClient}
}

func (h *Handler) Close() {
	h.mu.Lock()
	h.closed = true
	accounts := h.accounts
	h.accounts = make(map[string]*accountHandler)
	h.mu.Unlock()
	for _, entry := range accounts {
		entry.handler.Close()
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok {
		unauthorized(w)
		return
	}
	entry, err := h.account(username, password)
	if errors.Is(err, auth.ErrUnauthorized) {
		unauthorized(w)
		return
	}
	if err != nil {
		log.Printf("CalDAV authentication failed: %v", err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}
	recorder := &responseStatus{ResponseWriter: w, code: http.StatusOK}
	entry.handler.ServeHTTP(recorder, r)
	if recorder.code >= 200 && recorder.code < 300 && r.Method != http.MethodOptions {
		h.mu.Lock()
		entry.lastClient = time.Now()
		h.mu.Unlock()
	}
}

type responseStatus struct {
	http.ResponseWriter
	code int
}

func (r *responseStatus) WriteHeader(code int) { r.code = code; r.ResponseWriter.WriteHeader(code) }

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="ioxide"`)
	http.Error(w, "credentials required", http.StatusUnauthorized)
}
