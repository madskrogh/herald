package delivery_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/telemetry/telemetrytest"
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
// Run cancels the Attempt at the deadline, and releases its lease without
// counting it, so the Delivery is due again at once.
func TestWorkersRunCancelAttemptsAtTheDrainDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, 1)
		w := newPipeWorkers(t, store, respondNever(t))
		ctx, cancel := context.WithCancel(t.Context())
		done := runInBackground(ctx, w)

		time.Sleep(time.Second)
		// The Attempt is waiting for its response.
		synctest.Wait()
		cancel()
		cancelledAt := time.Now()
		<-done

		if got := time.Since(cancelledAt); got != drainTimeout {
			t.Errorf("Run returned %s after cancel, want %s", got, drainTimeout)
		}
		if got := store.recordedAttempts(); len(got) != 0 {
			t.Errorf("recorded %+v, want no Attempt", got)
		}
		want := []time.Time{cancelledAt.Add(drainTimeout)}
		if diff := cmp.Diff(want, store.releasedAt()); diff != "" {
			t.Errorf("leases released, due again at (-want +got):\n%s", diff)
		}
	})
}

// TestWorkersGiveUpOnAnAttemptAfter15Seconds makes an Attempt to an Endpoint
// that never answers: it ends after 15 seconds, recorded without a status, to
// be retried.
func TestWorkersGiveUpOnAnAttemptAfter15Seconds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, 1)
		w := newPipeWorkers(t, store, respondNever(t))
		start := time.Now()

		if _, err := w.RunOnce(t.Context()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		w.Wait()

		got := store.recordedAttempts()
		if len(got) != 1 {
			t.Fatalf("recorded %d Attempts, want 1", len(got))
		}
		r := got[0]
		if r.state != delivery.StatePending || r.attempt.StatusCode != 0 || r.attempt.Error == "" {
			t.Errorf("recorded %+v, want a pending Delivery and an Attempt with an error and no status", r)
		}
		if r.attempt.Latency != 15*time.Second {
			t.Errorf("latency = %s, want 15s", r.attempt.Latency)
		}
		if want := start.Add(20 * time.Second); !r.next.Equal(want) {
			t.Errorf("next Attempt at %s, want %s: 5 s after the timeout", r.next, want)
		}
	})
}

// TestWorkersRunDrainFor5SecondsByDefault leaves DrainTimeout unset: the
// Attempts in flight at shutdown still get 5 seconds, not none.
func TestWorkersRunDrainFor5SecondsByDefault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, 1)
		w := newRawPipeWorkers(t, store, func(conn net.Conn) { serveConn(conn, respondNever(t)) }, 0)
		ctx, cancel := context.WithCancel(t.Context())
		done := runInBackground(ctx, w)

		time.Sleep(time.Second)
		synctest.Wait()
		cancel()
		cancelledAt := time.Now()
		<-done

		if got := time.Since(cancelledAt); got != 5*time.Second {
			t.Errorf("Run returned %s after cancel, want 5s", got)
		}
	})
}

// TestWorkersRunRecordAResponseCutOffAtTheDrainDeadline has the Endpoint
// answer 200 but keep its body coming past the drain deadline. The Endpoint
// has answered, so the Attempt is recorded as a success with the body read so
// far, not released to be sent again.
func TestWorkersRunRecordAResponseCutOffAtTheDrainDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore(t, 1)
		// Promises 100 bytes of body and sends 7; the connection closes when
		// the client gives up on the rest.
		w := newRawPipeWorkers(t, store, func(conn net.Conn) {
			defer conn.Close()
			r := bufio.NewReader(conn)
			req, err := http.ReadRequest(r)
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial")
			_, _ = io.Copy(io.Discard, r)
		}, drainTimeout)
		ctx, cancel := context.WithCancel(t.Context())
		done := runInBackground(ctx, w)

		time.Sleep(time.Second)
		synctest.Wait()
		cancel()
		<-done

		if got := store.releasedAt(); len(got) != 0 {
			t.Errorf("released %d leases, want none: the Endpoint answered", len(got))
		}
		got := store.recordedAttempts()
		if len(got) != 1 || got[0].state != delivery.StateSucceeded || string(got[0].attempt.ResponseBody) != "partial" {
			t.Errorf("recorded %+v, want one succeeded Attempt with the body read so far", got)
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
	return newRawPipeWorkers(t, store, func(conn net.Conn) { serveConn(conn, handler) }, drainTimeout)
}

// newRawPipeWorkers is newPipeWorkers with serve speaking HTTP on each
// connection itself, for responses a handler can't produce, and the given
// DrainTimeout.
func newRawPipeWorkers(t *testing.T, store *fakeStore, serve func(net.Conn), drain time.Duration) *delivery.Workers {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		// Every connection closes after its request, so none is left
		// blocked when the bubble ends.
		DisableKeepAlives: true,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go serve(server)
			return client, nil
		},
	}}
	return delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:      store.keys,
		Client:       client,
		Schedule:     pinnedSchedule(t),
		Now:          time.Now,
		DrainTimeout: drain,

		TracerProvider: telemetrytest.NewRecorder(t).Provider,
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
// schedule and stop their Attempts and which addresses they connect to; the
// Store's tests cover the SQL.
type fakeStore struct {
	keys *keyring.Keyring

	mu       sync.Mutex
	due      []delivery.Lease
	claims   int
	recorded []recordedAttempt
	released []time.Time
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
func (s *fakeStore) Record(ctx context.Context, _ delivery.Lease, a delivery.Attempt, o delivery.Outcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, recordedAttempt{attempt: a, state: o.State, next: o.NextAttemptAt})
	return nil
}

// Release fails on a cancelled context, as a query does.
func (s *fakeStore) Release(ctx context.Context, _ delivery.Lease, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = append(s.released, now)
	return nil
}

// releasedAt returns when each released Delivery was made due again.
func (s *fakeStore) releasedAt() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.released)
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
