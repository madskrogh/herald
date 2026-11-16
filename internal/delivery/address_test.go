package delivery_test

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madskrogh/herald/internal/delivery"
)

// TestWorkersRefuseNonPublicAddresses makes an Attempt to an Endpoint on a
// loopback address with the Workers' own client. No connection is made, and
// the Attempt fails with the reason, to be retried like any other failure.
func TestWorkersRefuseNonPublicAddresses(t *testing.T) {
	t.Parallel()
	endpoint := newCountingEndpoint(t)
	store := newFakeStore(t, 1)
	store.due[0].Delivery.Endpoint.URL = endpoint.url + "/webhooks"
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	w := delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:  store.keys,
		Schedule: pinnedSchedule(t),
		Now:      func() time.Time { return now },
	}, slog.New(slog.DiscardHandler))

	if _, err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	w.Wait()

	if got := endpoint.connections.Load(); got != 0 {
		t.Errorf("Endpoint got %d connections, want none", got)
	}
	got := store.recordedAttempts()
	if len(got) != 1 {
		t.Fatalf("recorded %d Attempts, want 1", len(got))
	}
	r := got[0]
	if r.attempt.StatusCode != 0 || !strings.Contains(r.attempt.Error, "127.0.0.1 is loopback, not public") {
		t.Errorf("recorded Attempt with status %d and error %q, want no status and an error saying 127.0.0.1 is loopback", r.attempt.StatusCode, r.attempt.Error)
	}
	if want := now.Add(5 * time.Second); r.state != delivery.StatePending || !r.next.Equal(want) {
		t.Errorf("Delivery %s, next Attempt at %s, want pending and retried at %s", r.state, r.next, want)
	}
}

// TestWorkersAllowPrivateAddressesWhenConfigured is the local stack's
// relaxation: the same Endpoint gets the Attempt.
func TestWorkersAllowPrivateAddressesWhenConfigured(t *testing.T) {
	t.Parallel()
	endpoint := newCountingEndpoint(t)
	store := newFakeStore(t, 1)
	store.due[0].Delivery.Endpoint.URL = endpoint.url + "/webhooks"
	w := delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:               store.keys,
		Schedule:              pinnedSchedule(t),
		Now:                   time.Now,
		AllowPrivateAddresses: true,
	}, slog.New(slog.DiscardHandler))

	if _, err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	w.Wait()

	got := store.recordedAttempts()
	if len(got) != 1 || got[0].state != delivery.StateSucceeded {
		t.Errorf("recorded %+v, want one succeeded Attempt", got)
	}
}

// countingEndpoint is an Endpoint on a loopback address that answers 204 and
// counts the connections made to it.
type countingEndpoint struct {
	url         string
	connections atomic.Int64
}

func newCountingEndpoint(t *testing.T) *countingEndpoint {
	t.Helper()
	e := &countingEndpoint{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			e.connections.Add(1)
		}
	}
	// No idle connection is left in the Workers' client for the leak check.
	srv.Config.SetKeepAlivesEnabled(false)
	srv.Start()
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}
