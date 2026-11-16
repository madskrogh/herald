package delivery_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/madskrogh/herald/internal/delivery"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/retry"
	"github.com/madskrogh/herald/internal/signing"
)

// The signing secrets of the Endpoint in workerBatch, as during a rotation,
// and the keyring that encrypts them at rest.
var signingSecrets = []string{
	"whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw",
	// base64("second-signing-secret-key")
	"whsec_c2Vjb25kLXNpZ25pbmctc2VjcmV0LWtleQ==",
}

const testKeyring = "test:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestWorkersSignEveryAttempt(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 500, 200)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, rcv.URL()))

	tw.runOnce(t)
	tw.clock.set(now.Add(5 * time.Second))
	tw.runOnce(t)

	requests := rcv.received()
	if len(requests) != 2 {
		t.Fatalf("receiver got %d requests, want 2", len(requests))
	}
	for i, req := range requests {
		signedAt := []time.Time{now, now.Add(5 * time.Second)}[i]
		if got := string(req.body); got != `{"invoice": 42}` {
			t.Errorf("request %d: body = %q, want the payload byte for byte", i+1, got)
		}
		if got := req.header.Get("webhook-id"); got != messageID1 {
			t.Errorf("request %d: webhook-id = %q, want the Message ID %s", i+1, got, messageID1)
		}
		if got := req.header.Get("Content-Type"); got != "application/json" {
			t.Errorf("request %d: Content-Type = %q, want application/json", i+1, got)
		}
		for j, secret := range signingSecrets {
			if err := signing.Verify(secret, req.header, req.body, signedAt); err != nil {
				t.Errorf("request %d: verify with signing secret %d: %v", i+1, j, err)
			}
		}
	}
}

// TestWorkersMakeNoAttemptWithoutTheKey covers a signing secret encrypted
// with a key this dispatcher's keyring doesn't have: no unsigned request is
// sent, and the Attempt is recorded with the error and retried.
func TestWorkersMakeNoAttemptWithoutTheKey(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 200)
	tw := newTestWorkers(t)
	batch := tw.workerBatch(t, rcv.URL())
	other, err := keyring.Parse("other:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	ciphertext, err := other.Encrypt([]byte(signingSecrets[0]), []byte(endpointID1))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	batch.Messages[0].Deliveries[0].Endpoint.SigningSecrets = []*handoffv1.SigningSecret{{Ciphertext: ciphertext}}
	tw.in.send(t, batch)

	tw.runOnce(t)

	if n := rcv.count(); n != 0 {
		t.Errorf("receiver got %d requests, want none", n)
	}
	got := attempts(t, tw.in.store, uuid.MustParse(deliveryID1))
	if len(got) != 1 || got[0].StatusCode != 0 || !strings.Contains(got[0].Error, keyring.ErrUnknownKey.Error()) {
		t.Errorf("Attempts = %+v, want one without a status and with an unknown-key error", got)
	}
	d, err := tw.in.store.Delivery(t.Context(), uuid.MustParse(deliveryID1))
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if d.State != delivery.StatePending || !d.NextAttemptAt.Equal(now.Add(5*time.Second)) {
		t.Errorf("Delivery is %q, due at %s; want pending, due at now+5s", d.State, d.NextAttemptAt)
	}
}

func TestWorkersRetryUntilAnAttemptSucceeds(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 500, 503, 204)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, rcv.URL()))

	// Each failed Attempt schedules the next on the retry schedule, counted
	// from the end of the Attempt: 5 seconds after the first, a minute after
	// the second. Time stands still during an Attempt here.
	steps := []struct {
		at       time.Time
		wantNext time.Time
		want     delivery.State
	}{
		{at: now, wantNext: now.Add(5 * time.Second), want: delivery.StatePending},
		{at: now.Add(5 * time.Second), wantNext: now.Add(65 * time.Second), want: delivery.StatePending},
		{at: now.Add(65 * time.Second), want: delivery.StateSucceeded},
	}
	for i, step := range steps {
		tw.clock.set(step.at)
		tw.runOnce(t)

		got, err := tw.in.store.Delivery(t.Context(), uuid.MustParse(deliveryID1))
		if err != nil {
			t.Fatalf("Delivery: %v", err)
		}
		if got.State != step.want {
			t.Errorf("after Attempt %d: state = %q, want %q", i+1, got.State, step.want)
		}
		if step.want == delivery.StatePending && !got.NextAttemptAt.Equal(step.wantNext) {
			t.Errorf("after Attempt %d: next Attempt at %s, want %s", i+1, got.NextAttemptAt, step.wantNext)
		}
	}
	want := []delivery.Attempt{
		{Number: 1, StartedAt: now, StatusCode: 500, ResponseBody: []byte("status 500")},
		{Number: 2, StartedAt: now.Add(5 * time.Second), StatusCode: 503, ResponseBody: []byte("status 503")},
		{Number: 3, StartedAt: now.Add(65 * time.Second), StatusCode: 204, ResponseBody: []byte{}},
	}
	if diff := cmp.Diff(want, attempts(t, tw.in.store, uuid.MustParse(deliveryID1)), ignoreLatency); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
}

func TestWorkersFailADeliveryAfter8Attempts(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 500)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, rcv.URL()))

	// When each Attempt starts: each follows the one before by the retry
	// schedule's delay (docs/design.md: immediately, 5 s, 1 m, 10 m, 1 h,
	// 3 h, 6 h, 12 h).
	starts := []time.Time{
		now,
		now.Add(5 * time.Second),
		now.Add(65 * time.Second),
		now.Add(11*time.Minute + 5*time.Second),
		now.Add(1*time.Hour + 11*time.Minute + 5*time.Second),
		now.Add(4*time.Hour + 11*time.Minute + 5*time.Second),
		now.Add(10*time.Hour + 11*time.Minute + 5*time.Second),
		now.Add(22*time.Hour + 11*time.Minute + 5*time.Second),
	}
	for _, at := range starts {
		tw.clock.set(at)
		tw.runOnce(t)
	}
	// Nothing is left to claim, however late.
	tw.clock.set(now.Add(72 * time.Hour))
	tw.runOnce(t)

	got, err := tw.in.store.Delivery(t.Context(), uuid.MustParse(deliveryID1))
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StateFailed {
		t.Errorf("state = %q, want %q", got.State, delivery.StateFailed)
	}
	var want []delivery.Attempt
	for i, at := range starts {
		want = append(want, delivery.Attempt{Number: i + 1, StartedAt: at, StatusCode: 500, ResponseBody: []byte("status 500")})
	}
	if diff := cmp.Diff(want, attempts(t, tw.in.store, uuid.MustParse(deliveryID1)), ignoreLatency); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
	if n := rcv.count(); n != 8 {
		t.Errorf("receiver got %d requests, want 8", n)
	}
}

// ignoreLatency leaves out an Attempt's latency, which is real time even
// under the fake clock.
var ignoreLatency = cmpopts.IgnoreFields(delivery.Attempt{}, "Latency")

// testWorkers is Workers with a fake clock, on a database of their own, fed
// by an intake.
type testWorkers struct {
	in      *testIntake
	clock   *fakeClock
	keys    *keyring.Keyring
	workers *delivery.Workers
}

func newTestWorkers(t *testing.T) *testWorkers {
	t.Helper()
	keys, err := keyring.Parse(testKeyring)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	in := newTestIntake(t.Context(), t)
	clock := &fakeClock{t: now}
	return &testWorkers{
		in:      in,
		clock:   clock,
		keys:    keys,
		workers: newWorkers(t, in.store, keys, clock),
	}
}

// newWorkers returns Workers on store with the retry schedule's jitter
// pinned, so every delay is exact.
func newWorkers(t *testing.T, store *delivery.Store, keys *keyring.Keyring, clock *fakeClock) *delivery.Workers {
	t.Helper()
	schedule, err := retry.New(1, func() float64 { return 0.5 })
	if err != nil {
		t.Fatalf("retry schedule: %v", err)
	}
	return delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:  keys,
		Client:   &http.Client{},
		Schedule: schedule,
		Now:      clock.now,
	}, slog.New(slog.DiscardHandler))
}

// runOnce runs one round of the workers and waits for its Attempts.
func (tw *testWorkers) runOnce(t *testing.T) {
	t.Helper()
	if _, err := tw.workers.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	tw.workers.Wait()
}

// workerBatch returns a Batch of one Message with one Delivery, deliveryID1,
// to an Endpoint at url whose signing secrets are signingSecrets.
func (tw *testWorkers) workerBatch(t *testing.T, url string) *handoffv1.Batch {
	t.Helper()
	var secrets []*handoffv1.SigningSecret
	for i, s := range signingSecrets {
		ciphertext, err := tw.keys.Encrypt([]byte(s), []byte(endpointID1))
		if err != nil {
			t.Fatalf("encrypt signing secret %d: %v", i, err)
		}
		secrets = append(secrets, &handoffv1.SigningSecret{Ciphertext: ciphertext})
	}
	return &handoffv1.Batch{Messages: []*handoffv1.Message{{
		Id:        messageID1,
		TenantId:  tenantID,
		EventType: "invoice.paid",
		Payload:   []byte(`{"invoice": 42}`),
		Deliveries: []*handoffv1.Delivery{{
			Id: deliveryID1,
			Endpoint: &handoffv1.Endpoint{
				Id:             endpointID1,
				Url:            url,
				SigningSecrets: secrets,
			},
		}},
	}}}
}

// fakeClock is a clock that tests set.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// receiver is an Endpoint that answers with the given status codes in turn,
// repeating the last, and keeps the requests it gets. Error statuses come
// with the body "status <code>".
type receiver struct {
	srv *httptest.Server

	mu       sync.Mutex
	statuses []int
	requests []receivedRequest
}

type receivedRequest struct {
	header http.Header
	body   []byte
}

func newReceiver(t *testing.T, statuses ...int) *receiver {
	t.Helper()
	r := &receiver{statuses: statuses}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) URL() string { return r.srv.URL + "/webhooks" }

// received returns the requests the receiver got, in order.
func (r *receiver) received() []receivedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

// count returns how many requests the receiver got.
func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) serve(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	status := r.statuses[min(len(r.requests), len(r.statuses)-1)]
	r.requests = append(r.requests, receivedRequest{header: req.Header.Clone(), body: body})
	r.mu.Unlock()
	w.WriteHeader(status)
	if status >= 300 {
		fmt.Fprintf(w, "status %d", status)
	}
}
