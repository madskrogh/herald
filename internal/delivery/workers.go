package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/retry"
	"github.com/madskrogh/herald/internal/signing"
)

const (
	// attemptTimeout is how long an Endpoint has to answer an Attempt
	// (docs/design.md, Delivery semantics). It stays well under
	// leaseDuration, so a live worker records its Attempt before its lease
	// runs out.
	attemptTimeout = 15 * time.Second
	// maxResponseBody is how much of a response body an Attempt keeps.
	maxResponseBody = 4 << 10
	// defaultConcurrency is the most Attempts one process has in flight when
	// WorkersConfig.Concurrency is zero.
	defaultConcurrency = 32
)

// WorkersConfig configures Workers.
type WorkersConfig struct {
	// Keyring decrypts the Endpoints' signing secrets.
	Keyring *keyring.Keyring
	// Client makes the Attempts. Its own timeout, if any, applies on top of
	// attemptTimeout.
	Client *http.Client
	// Schedule says when each Attempt after a failed one is due.
	Schedule retry.Schedule
	// Now tells the current time: when Deliveries are due, the time each
	// Attempt is signed with, and when the next is due.
	Now func() time.Time
	// Concurrency is the most Attempts in flight in this process.
	Concurrency int
}

// Workers make the Attempts for due Deliveries: each signed, recorded, and
// followed by another on the retry schedule until one succeeds or the
// schedule runs out.
type Workers struct {
	store  *Store
	cfg    WorkersConfig
	logger *slog.Logger

	// round makes RunOnce calls take turns, so they don't claim more than
	// Concurrency between them.
	round sync.Mutex
	// mu guards inFlight.
	mu       sync.Mutex
	inFlight int
	attempts sync.WaitGroup
}

// NewWorkers returns Workers that claim Deliveries from store.
func NewWorkers(store *Store, cfg WorkersConfig, logger *slog.Logger) *Workers {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultConcurrency
	}
	return &Workers{store: store, cfg: cfg, logger: logger}
}

// RunOnce claims as many due Deliveries as there are free workers and starts
// their Attempts, without waiting for them. It returns how many it started.
// The Attempts run with ctx's values but not its cancellation; Wait waits for
// them.
func (w *Workers) RunOnce(ctx context.Context) (int, error) {
	w.round.Lock()
	defer w.round.Unlock()

	w.mu.Lock()
	free := w.cfg.Concurrency - w.inFlight
	w.mu.Unlock()
	if free <= 0 {
		return 0, nil
	}
	leases, err := w.store.Claim(ctx, w.cfg.Now(), free)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.inFlight += len(leases)
	w.mu.Unlock()
	attemptCtx := context.WithoutCancel(ctx)
	for _, lease := range leases {
		w.attempts.Go(func() {
			defer func() {
				w.mu.Lock()
				w.inFlight--
				w.mu.Unlock()
			}()
			w.attempt(attemptCtx, lease)
		})
	}
	return len(leases), nil
}

// Wait waits for the Attempts that RunOnce started.
func (w *Workers) Wait() {
	w.attempts.Wait()
}

// attempt makes one Attempt for the leased Delivery and records it.
func (w *Workers) attempt(ctx context.Context, lease Lease) {
	d := lease.Delivery
	a := w.send(ctx, d)
	number := lease.Attempts + 1
	state, next := w.outcome(a, number)
	logger := w.logger.With(
		"delivery_id", d.ID,
		"message_id", d.MessageID,
		"endpoint_id", d.Endpoint.ID,
		"attempt", number,
		"status_code", a.StatusCode,
		"latency_ms", a.Latency.Milliseconds(),
		"state", state,
	)
	if a.Error != "" {
		logger = logger.With("attempt_error", a.Error)
	}
	if err := w.store.Record(ctx, lease, a, state, next); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			// Another worker claimed the Delivery after this lease ran out,
			// and its Attempt counts instead.
			logger.WarnContext(ctx, "attempt not recorded: lease ran out", "error", err)
			return
		}
		// The lease runs out and the Delivery is claimed again.
		logger.ErrorContext(ctx, "attempt not recorded", "error", err)
		return
	}
	logger.InfoContext(ctx, "attempt")
}

// send makes the HTTP request for one Attempt, signed with the Endpoint's
// signing secrets. A request that can't be made or gets no response is an
// Attempt with an error and no status code.
func (w *Workers) send(ctx context.Context, d Delivery) Attempt {
	a := Attempt{StartedAt: w.cfg.Now()}
	req, err := w.newRequest(ctx, d, a.StartedAt)
	if err != nil {
		a.Error = err.Error()
		return a
	}
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	begin := time.Now()
	resp, err := w.cfg.Client.Do(req.WithContext(ctx))
	if err != nil {
		a.Latency = time.Since(begin)
		a.Error = err.Error()
		return a
	}
	defer resp.Body.Close()
	a.Latency = time.Since(begin)
	a.StatusCode = resp.StatusCode
	// A body cut short by the timeout is kept as far as it got.
	a.ResponseBody, _ = io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	return a
}

// newRequest returns the signed request for an Attempt at ts. The signing
// secrets are decrypted only here, just before signing, and the webhook-id is
// the Message's ID, the same for every Attempt and every Endpoint.
func (w *Workers) newRequest(ctx context.Context, d Delivery, ts time.Time) (*http.Request, error) {
	secrets := make([]string, 0, len(d.Endpoint.SigningSecrets))
	for i, ciphertext := range d.Endpoint.SigningSecrets {
		secret, err := w.cfg.Keyring.Decrypt(ciphertext, []byte(d.Endpoint.ID.String()))
		if err != nil {
			return nil, fmt.Errorf("decrypt signing secret %d: %w", i, err)
		}
		secrets = append(secrets, string(secret))
	}
	header, err := signing.Sign(d.MessageID.String(), ts, d.Payload, secrets)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint.URL, bytes.NewReader(d.Payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for name, values := range header {
		req.Header[name] = values
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Herald")
	return req, nil
}

// outcome returns the state an Attempt moves its Delivery to and, for a
// pending Delivery, when the next Attempt is due. number is the Attempt's
// number.
func (w *Workers) outcome(a Attempt, number int) (State, time.Time) {
	if a.StatusCode >= 200 && a.StatusCode < 300 {
		return StateSucceeded, time.Time{}
	}
	delay, ok := w.cfg.Schedule.Next(number)
	if !ok {
		return StateFailed, time.Time{}
	}
	return StatePending, w.cfg.Now().Add(delay)
}
