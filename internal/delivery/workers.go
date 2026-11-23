package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/madskrogh/herald/internal/egress"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/retry"
	"github.com/madskrogh/herald/internal/signing"
	"github.com/madskrogh/herald/internal/telemetry"
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
	// defaultDrainTimeout is WorkersConfig.DrainTimeout when it is zero.
	defaultDrainTimeout = 5 * time.Second
	// recordTimeout bounds recording an Attempt or releasing its lease,
	// which runs even after the Attempt was cancelled at shutdown.
	recordTimeout = 2 * time.Second
)

// How Run paces its rounds (see poll).
const (
	pollInterval = 500 * time.Millisecond
	maxBackoff   = 30 * time.Second
)

// WorkersConfig configures Workers.
type WorkersConfig struct {
	// Keyring decrypts the Endpoints' signing secrets.
	Keyring *keyring.Keyring
	// Client makes the Attempts, or nil for one that keeps a connection to
	// each Endpoint for every Attempt it may have in flight and refuses to
	// connect to non-public addresses (see AllowPrivateAddresses). A Client
	// given here is used as it is, without that check. Its own timeout, if
	// any, applies on top of attemptTimeout. Workers never follow redirects,
	// whatever its CheckRedirect.
	Client *http.Client
	// Schedule says when each Attempt after a failed one is due.
	Schedule retry.Schedule
	// Now tells the current time: when Deliveries are due, the time each
	// Attempt is signed with, and when the next is due.
	Now func() time.Time
	// Concurrency is the most Attempts in flight in this process.
	Concurrency int
	// DrainTimeout is how long the Attempts in flight get to finish once
	// Run's context is cancelled, or zero for 5 seconds.
	DrainTimeout time.Duration
	// AllowPrivateAddresses lets the Workers' own client connect to
	// loopback, link-local, private and other non-public addresses. It is
	// for the local stack and tests only, whose Endpoints are on such
	// addresses. herald-dispatcher sets it from
	// HERALD_ALLOW_INSECURE_ENDPOINTS.
	AllowPrivateAddresses bool
	// TracerProvider makes the span of each Attempt, or nil for none.
	TracerProvider trace.TracerProvider
}

// leaseStore is where Workers claim Deliveries and record their Attempts. It
// is a *Store, except in tests of how Workers schedule and stop Attempts.
type leaseStore interface {
	Claim(ctx context.Context, now time.Time, limit int) ([]Lease, error)
	Record(ctx context.Context, lease Lease, a Attempt, o Outcome) error
	Release(ctx context.Context, lease Lease, now time.Time) error
}

// Workers make the Attempts for due Deliveries: each signed, recorded, and
// followed by another on the retry schedule until one succeeds or the
// schedule runs out.
type Workers struct {
	store  leaseStore
	cfg    WorkersConfig
	tracer trace.Tracer
	logger *slog.Logger

	// claiming makes rounds take turns, so they don't claim more than
	// Concurrency between them.
	claiming sync.Mutex
	// mu guards inFlight.
	mu       sync.Mutex
	inFlight int
	attempts sync.WaitGroup
}

// NewWorkers returns Workers that claim Deliveries from store.
func NewWorkers(store leaseStore, cfg WorkersConfig, logger *slog.Logger) *Workers {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultConcurrency
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = defaultDrainTimeout
	}
	if cfg.TracerProvider == nil {
		cfg.TracerProvider = noop.NewTracerProvider()
	}
	var client http.Client
	if cfg.Client != nil {
		// A copy, so the caller's client is left as it is.
		client = *cfg.Client
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		// Keep a connection for every Attempt an Endpoint may have in
		// flight, not the default 2.
		transport.MaxIdleConnsPerHost = maxInFlight
		// Connect to each Endpoint directly. Through a proxy, the address
		// check below would see only the proxy's address.
		transport.Proxy = nil
		// The timeouts are those of http.DefaultTransport's dialer, which
		// this one replaces.
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		if !cfg.AllowPrivateAddresses {
			dialer.Control = egress.Control
		}
		transport.DialContext = dialer.DialContext
		client.Transport = transport
	}
	// Only a 2xx response is a success, so a redirect is an Attempt that
	// failed, not a pointer to follow.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.Client = &client
	return &Workers{store: store, cfg: cfg, tracer: cfg.TracerProvider.Tracer(tracerName), logger: logger}
}

// Run makes Attempts until ctx is cancelled, claiming due Deliveries whenever
// workers are free. Once ctx is cancelled it claims no more, and gives the
// Attempts in flight DrainTimeout to finish. It then cancels those still
// running, releases their leases without counting them, so their Deliveries
// are due again at once, and returns.
func (w *Workers) Run(ctx context.Context) {
	attemptCtx, cancelAttempts := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelAttempts()
	w.poll(ctx, attemptCtx)

	drained := make(chan struct{})
	go func() {
		w.attempts.Wait()
		close(drained)
	}()
	timer := time.NewTimer(w.cfg.DrainTimeout)
	defer timer.Stop()
	select {
	case <-drained:
	case <-timer.C:
		w.logger.WarnContext(ctx, "cancelling attempts still in flight at shutdown")
		cancelAttempts()
		<-drained
	}
}

// poll runs rounds until ctx is cancelled. A round that filled every free
// worker is followed by the next at once, to work through a backlog.
// Otherwise the next waits pollInterval, and after a failure longer still:
// the wait doubles with each failure in a row, up to maxBackoff.
func (w *Workers) poll(ctx, attemptCtx context.Context) {
	backoff := pollInterval
	for {
		full, err := w.round(ctx, attemptCtx)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		switch {
		case err != nil:
			w.logger.WarnContext(ctx, "delivery round failed", "error", err, "retry_in", backoff)
			wait = backoff
			backoff = min(2*backoff, maxBackoff)
		case full:
			backoff = pollInterval
			continue
		default:
			backoff = pollInterval
			wait = pollInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// RunOnce claims as many due Deliveries as there are free workers and starts
// their Attempts, without waiting for them. It returns how many it started.
// The Attempts run with ctx's values but not its cancellation; Wait waits for
// them.
func (w *Workers) RunOnce(ctx context.Context) (int, error) {
	started, _, err := w.claimAndStart(ctx, context.WithoutCancel(ctx))
	return started, err
}

// round is one round of Run: it reports whether it filled every free worker,
// in which case more Deliveries may be due.
func (w *Workers) round(ctx, attemptCtx context.Context) (full bool, err error) {
	started, free, err := w.claimAndStart(ctx, attemptCtx)
	return free > 0 && started == free, err
}

// claimAndStart claims as many due Deliveries as there are free workers, with
// ctx, and starts their Attempts with attemptCtx. It returns how many it
// started, and how many workers were free.
func (w *Workers) claimAndStart(ctx, attemptCtx context.Context) (started, free int, err error) {
	w.claiming.Lock()
	defer w.claiming.Unlock()

	w.mu.Lock()
	free = w.cfg.Concurrency - w.inFlight
	w.mu.Unlock()
	if free <= 0 {
		return 0, 0, nil
	}
	leases, err := w.store.Claim(ctx, w.cfg.Now(), free)
	if err != nil {
		return 0, free, err
	}
	w.mu.Lock()
	w.inFlight += len(leases)
	w.mu.Unlock()
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
	return len(leases), free, nil
}

// Wait waits for the Attempts that RunOnce started.
func (w *Workers) Wait() {
	w.attempts.Wait()
}

// attempt makes one Attempt for the leased Delivery and records it. An
// Attempt that Run cancels at the shutdown deadline isn't counted: its lease
// is released, so the Delivery is due again at once.
//
// The Attempt's span is a child of the span that stored the Delivery, so
// every Attempt joins the Message's trace, whenever and wherever it runs.
func (w *Workers) attempt(ctx context.Context, lease Lease) {
	d := lease.Delivery
	number := lease.PriorAttempts + 1
	lateness := w.cfg.Now().Sub(lease.DueAt)
	ctx, span := telemetry.Start(ctx, w.tracer, d.TraceParent, telemetry.SpanAttempt,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			telemetry.TenantID.String(d.TenantID.String()),
			telemetry.ApplicationID.String(applicationIDAttr(d.ApplicationID)),
			telemetry.EndpointID.String(d.Endpoint.ID.String()),
			telemetry.MessageID.String(d.MessageID.String()),
			telemetry.DeliveryID.String(d.ID.String()),
			telemetry.AttemptNumber.Int(number),
			telemetry.LatenessMs.Int64(lateness.Milliseconds()),
		),
	)
	a, attemptErr := w.send(ctx, d)
	span.SetAttributes(
		telemetry.StatusCode.Int(a.StatusCode),
		telemetry.LatencyMs.Int64(a.Latency.Milliseconds()),
	)
	logger := w.logger.With(
		"delivery_id", d.ID,
		"message_id", d.MessageID,
		"endpoint_id", d.Endpoint.ID,
		"attempt", number,
	)
	// ctx is cancelled only at the shutdown deadline; the store must still
	// hear how the Attempt ended.
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	// Cut off without a response. One that came, even with its body cut
	// short, is the Endpoint's answer and is recorded.
	if a.StatusCode == 0 && ctx.Err() != nil {
		telemetry.End(span, telemetry.OutcomeCutOff, attemptErr)
		if err := w.store.Release(storeCtx, lease, w.cfg.Now()); err != nil {
			// The lease runs out and the Delivery is claimed again.
			logger.ErrorContext(ctx, "lease not released at shutdown", "error", err)
			return
		}
		logger.WarnContext(ctx, "attempt cut off at shutdown, not counted")
		return
	}
	o := w.outcome(a, number)
	logger = logger.With(
		"status_code", a.StatusCode,
		"latency_ms", a.Latency.Milliseconds(),
		"state", o.State,
	)
	if a.Error != "" {
		logger = logger.With("attempt_error", a.Error)
	}
	if err := w.store.Record(storeCtx, lease, a, o); err != nil {
		// The Delivery stays where it was, so its state is no outcome.
		telemetry.End(span, telemetry.OutcomeNotRecorded, err)
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
	telemetry.End(span, string(o.State), attemptErr)
	logger.InfoContext(ctx, "attempt")
}

// send makes the HTTP request for one Attempt, signed with the Endpoint's
// signing secrets. A request that can't be made or gets no response is an
// Attempt with an error and no status code. The error returned is for the
// Attempt's span: why it isn't a success, without the Endpoint's URL. The
// Tenant sees the Attempt's error, but spans leave Herald, and a URL often
// holds a token.
func (w *Workers) send(ctx context.Context, d Delivery) (Attempt, error) {
	a := Attempt{StartedAt: w.cfg.Now()}
	req, err := w.newRequest(ctx, d, a.StartedAt)
	if err != nil {
		a.Error = err.Error()
		return a, withoutURL(err)
	}
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	begin := time.Now()
	resp, err := w.cfg.Client.Do(req.WithContext(ctx))
	if err != nil {
		a.Latency = time.Since(begin)
		a.Error = err.Error()
		// Tenants see the Attempt's error. The dial error names the address
		// the Endpoint's host resolved to, which for a refused address is
		// one inside Herald's network.
		var refused *egress.RefusedError
		if errors.As(err, &refused) {
			a.Error = fmt.Sprintf("not connected: the Endpoint's host resolved to a %s address, and Herald connects only to public addresses", refused.Class)
			return a, errors.New(a.Error)
		}
		return a, withoutURL(err)
	}
	defer resp.Body.Close()
	a.Latency = time.Since(begin)
	a.StatusCode = resp.StatusCode
	// A body cut short by the timeout is kept as far as it got.
	a.ResponseBody, _ = io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if !succeeded(a.StatusCode) {
		return a, fmt.Errorf("the Endpoint answered %d %s", a.StatusCode, http.StatusText(a.StatusCode))
	}
	return a, nil
}

// succeeded reports whether an Attempt's status code makes it a success:
// only a 2xx response is.
func succeeded(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}

// withoutURL returns err without the URL that net/url and net/http errors
// name, keeping the operation and why it failed.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
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
		return nil, fmt.Errorf("sign: %w", err)
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

// outcome returns where an Attempt leaves its Delivery. number is the
// Attempt's number.
func (w *Workers) outcome(a Attempt, number int) Outcome {
	if succeeded(a.StatusCode) {
		return Succeeded()
	}
	delay, ok := w.cfg.Schedule.Next(number)
	if !ok {
		return Failed()
	}
	return RetryAt(w.cfg.Now().Add(delay))
}
