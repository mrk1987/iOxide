package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mrk1987/ioxide/auth"
	"github.com/mrk1987/ioxide/caldav"
)

func cmdServe(opts *options) error {
	if (opts.tlsCert == "") != (opts.tlsKey == "") {
		return errors.New("-tls-cert and -tls-key must be given together")
	}

	var handler http.Handler = newServer(auth.NewManager(opts.newClient))
	if opts.logRequests {
		handler = logRequests(handler)
	}
	srv := &http.Server{
		Addr:              opts.listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		if opts.tlsCert != "" {
			cert, err := tls.LoadX509KeyPair(opts.tlsCert, opts.tlsKey)
			if err != nil {
				errc <- fmt.Errorf("loading TLS key pair: %w", err)
				return
			}
			srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
			log.Printf("CalDAV server listening with TLS on %s", srv.Addr)
			errc <- srv.ListenAndServeTLS("", "")
			return
		}
		log.Printf("CalDAV server listening on %s", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// server authenticates every request with HTTP Basic auth against the stored
// accounts and routes it to a per-user CalDAV handler.
type server struct {
	auth *auth.Manager

	mu       sync.Mutex
	handlers map[string]http.Handler
}

func newServer(manager *auth.Manager) *server {
	return &server{auth: manager, handlers: map[string]http.Handler{}}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok {
		s.unauthorized(w)
		return
	}

	session, err := s.auth.Auth(username, password)
	if errors.Is(err, auth.ErrUnauthorized) {
		s.unauthorized(w)
		return
	}
	if err != nil {
		log.Printf("authenticating %s: %v", username, err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	h, ok := s.handlers[username]
	if !ok {
		h, err = caldav.NewHandler(session.Client, session.PrivateKeys, username, &session.CacheKey)
		if err == nil {
			s.handlers[username] = h
		}
	}
	s.mu.Unlock()
	if err != nil {
		log.Printf("starting CalDAV handler for %s: %v", username, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.ServeHTTP(w, r)
}

func (s *server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="ioxide"`)
	http.Error(w, "credentials required", http.StatusUnauthorized)
}
