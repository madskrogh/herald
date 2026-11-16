package delivery_test

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/retry"
)

// drainTimeout is how long the Attempts in flight get to finish once Run's
// context is cancelled, in these tests.
const drainTimeout = 8 * time.Second

// TestWorkersRunFinishAttemptsInFlightAtShutdown cancels Run while an
// Attempt waits 3 seconds for its response: Run claims nothing more, and
// returns once the Attempt is recorded.
func TestWorkersRunFinishAttemptsInFlightAtShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, 1)
		w := newPipeWorkers(t, store, respondAfter(3*time.Second, http.StatusOK))
		ctx, cancel := context.WithCancel(t.Context())
		done := runInBackground(ctx, w)

		time.Sleep(time.Second)
		// Every round due by now has run, and Run waits for the next.
		synctest.Wait()
		claims := store.claimCount()
		cancel()
		cancelledAt := time.Now()
		<-done

		if got := time.Since(cancelledAt); got != 2*time.Second {
			t.Errorf("Run returned %s after cancel, want 2s, when the Attempt's response arrives", got)
		}
		if got := store.claimCount(); got != claims {
			t.Errorf("Run claimed %d more times after cancel, want none", got-claims)
		}
		got := store.recordedAttempts()
		if len(got) != 1 || got[0].state != delivery.StateSucceeded {
			t.Errorf("recorded %+v, want one succeeded Attempt", got)
		}
	})
}

// TestWorkersRunCancelAttemptsAtTheDrainDeadline cancels Run while an
// Attempt waits for a response that doesn't come before the drain timeout:
// Run cancels the Attempt at the deadline, and records it as failed, to be
// retried.
func TestWorkersRunCancelAttemptsAtTheDrainDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, 1)
		w := newPipeWorkers(t, store, respondNever(t))
		ctx, cancel := context.WithCancel(t.Context())
		done := runInBackground(ctx, w)

		time.Sleep(time.Second)
		cancel()
		cancelledAt := time.Now()
		<-done

		if got := time.Since(cancelledAt); got != drainTimeout {
			t.Errorf("Run returned %s after cancel, want %s", got, drainTimeout)
		}
		got := store.recordedAttempts()
		if len(got) != 1 {
			t.Fatalf("recorded %d Attempts, want 1", len(got))
		}
		r := got[0]
		if r.state != delivery.StatePending || r.attempt.StatusCode != 0 || !strings.Contains(r.attempt.Error, "context canceled") {
			t.Errorf("recorded %+v, want a pending Delivery and an Attempt cancelled without a response", r)
		}
		// Retried on the schedule, 5 s after the cancelled Attempt ended.
		if want := cancelledAt.Add(drainTimeout + 5*time.Second); !r.next.Equal(want) {
			t.Errorf("next Attempt at %s, want %s", r.next, want)
		}
	})
}

// runInBackground runs w until ctx is cancelled, and returns a channel that is
// closed when Run returns.
func runInBackground(ctx context.Context, w *delivery.Workers) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	return done
}

// newPipeWorkers returns Workers on store whose Attempts go to handler over
// in-memory connections, so they run on synctest's fake time.
func newPipeWorkers(t *testing.T, store *fakeStore, handler http.Handler) *delivery.Workers {
	t.Helper()
	schedule, err := retry.New(1, func() float64 { return 0.5 })
	if err != nil {
		t.Fatalf("retry schedule: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{
		// Every connection closes after its request, so none is left
		// blocked when the bubble ends.
		DisableKeepAlives: true,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go serveConn(server, handler)
			return client, nil
		},
	}}
	return delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:      store.keys,
		Client:       client,
		Schedule:     schedule,
		Now:          time.Now,
		DrainTimeout: drainTimeout,
	}, slog.New(slog.DiscardHandler))
}

// serveConn serves one HTTP request on conn with handler, and closes conn,
// which ends the response body.
func serveConn(conn net.Conn, handler http.Handler) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	_ = rec.Result().Write(conn)
}

// respondAfter returns a handler that answers with status after d.
func respondAfter(d time.Duration, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(d)
		w.WriteHeader(status)
	})
}

// respondNever returns a handler that doesn't answer until the test ends, so
// no goroutine is left in the bubble.
func respondNever(t *testing.T) http.Handler {
	end := make(chan struct{})
	t.Cleanup(func() { close(end) })
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-end })
}

// fakeStore hands out leases on a fixed set of Deliveries and keeps what is
// recorded for them. It stands in for Postgres only to test how Workers
// schedule and stop their Attempts; the Store's tests cover the SQL.
type fakeStore struct {
	keys *keyring.Keyring

	mu       sync.Mutex
	due      []delivery.Lease
	claims   int
	recorded []recordedAttempt
}

type recordedAttempt struct {
	attempt delivery.Attempt
	state   delivery.State
	next    time.Time
}

// newFakeStore returns a fakeStore with n Deliveries due.
func newFakeStore(t *testing.T, n int) *fakeStore {
	t.Helper()
	keys, err := keyring.Parse(testKeyring)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	s := &fakeStore{keys: keys}
	endpointID := uuid.MustParse(endpointID1)
	ciphertext, err := keys.Encrypt([]byte(signingSecrets[0]), []byte(endpointID1))
	if err != nil {
		t.Fatalf("encrypt signing secret: %v", err)
	}
	for range n {
		s.due = append(s.due, delivery.Lease{
			ID: uuid.New(),
			Delivery: delivery.Delivery{
				ID:        uuid.New(),
				MessageID: uuid.MustParse(messageID1),
				Payload:   []byte(`{"invoice": 42}`),
				Endpoint: delivery.Endpoint{
					ID:             endpointID,
					URL:            "http://endpoint.test/webhooks",
					SigningSecrets: [][]byte{ciphertext},
				},
				State: delivery.StatePending,
			},
		})
	}
	return s
}

func (s *fakeStore) Claim(_ context.Context, _ time.Time, limit int) ([]delivery.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	n := min(limit, len(s.due))
	leases := s.due[:n]
	s.due = s.due[n:]
	return leases, nil
}

// Record fails on a cancelled context, as a query does.
func (s *fakeStore) Record(ctx context.Context, _ delivery.Lease, a delivery.Attempt, state delivery.State, next time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, recordedAttempt{attempt: a, state: state, next: next})
	return nil
}

func (s *fakeStore) claimCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (s *fakeStore) recordedAttempts() []recordedAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedAttempt(nil), s.recorded...)
}
