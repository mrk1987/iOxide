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
	"syscall"
	"time"

	"github.com/mrk1987/ioxide/internal/auth"
	"github.com/mrk1987/ioxide/internal/bridge"
	"github.com/mrk1987/ioxide/internal/protonmail"
)

func cmdServe(opts *options) error {
	if (opts.tlsCert == "") != (opts.tlsKey == "") {
		return errors.New("-tls-cert and -tls-key must be given together")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	owned := bridge.NewHandler(ctx, auth.NewManager(func() *protonmail.Client {
		c := opts.newClient()
		c.Context = ctx
		return c
	}))
	defer func() { stop(); owned.Close() }()
	var handler http.Handler = owned
	if opts.logRequests {
		handler = logRequests(handler)
	}
	srv := &http.Server{
		Addr:              opts.listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

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

// newServer is retained for CLI tests; the desktop uses the same handler.
func newServer(manager *auth.Manager) *bridge.Handler {
	return bridge.NewHandler(context.Background(), manager)
}
