package caldav

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/mrk1987/ioxide/internal/protonmail"
)

type cancelTransport struct{ started chan struct{} }

func (c cancelTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestHandlerWorkersExitWithServerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	client := &protonmail.Client{Context: ctx, RootURL: "https://dummy.invalid", HTTPClient: &http.Client{Transport: cancelTransport{started}}}
	h, err := NewHandlerContext(ctx, client, openpgp.EntityList{&openpgp.Entity{}}, "sample", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not stop after cancellation")
	}
	h.Close() // Closing twice must be safe.
}
