package delivery_test

import (
	"bytes"
	"encoding/json"
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

// TestWorkersSendNothingUnsignedWithoutTheKey covers a signing secret encrypted
// with a key this dispatcher's keyring doesn't have: no unsigned request is
// sent, and the Attempt is recorded with the error and retried.
func TestWorkersSendNothingUnsignedWithoutTheKey(t *testing.T) {
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
	// schedule's delay (spec R-10, R-11 and retry-delays: immediately, 5 s,
	// 1 m, 10 m, 1 h, 3 h, 6 h, 12 h).
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

// TestWorkersKeepTheStartOfTheResponseBody checks that an Attempt keeps the
// first 4 KiB of a longer response body.
func TestWorkersKeepTheStartOfTheResponseBody(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	body := strings.Repeat("a", 4096) + strings.Repeat("b", 6000)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(endpoint.Close)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, endpoint.URL))

	tw.runOnce(t)

	got := attempts(t, tw.in.store, uuid.MustParse(deliveryID1))
	if len(got) != 1 {
		t.Fatalf("got %d Attempts, want 1", len(got))
	}
	if want := strings.Repeat("a", 4096); string(got[0].ResponseBody) != want {
		t.Errorf("response body is %d bytes, %d of them 'a'; want the first 4096 bytes, all 'a'", len(got[0].ResponseBody), strings.Count(string(got[0].ResponseBody), "a"))
	}
}

// TestWorkersDoNotFollowRedirects checks that a redirect is an Attempt that
// failed, as any status but 2xx is, even when the redirect leads to a 200.
func TestWorkersDoNotFollowRedirects(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	redirectedTo := newReceiver(t, 200)
	redirect := httptest.NewServer(http.RedirectHandler(redirectedTo.URL(), http.StatusFound))
	t.Cleanup(redirect.Close)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, redirect.URL))

	tw.runOnce(t)

	if n := redirectedTo.count(); n != 0 {
		t.Errorf("redirect target got %d requests, want none", n)
	}
	got, err := tw.in.store.Delivery(t.Context(), uuid.MustParse(deliveryID1))
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StatePending {
		t.Errorf("state = %q, want %q", got.State, delivery.StatePending)
	}
	if a := attempts(t, tw.in.store, uuid.MustParse(deliveryID1)); len(a) != 1 || a[0].StatusCode != http.StatusFound {
		t.Errorf("Attempts = %+v, want one with status 302", a)
	}
}

// TestWorkersRetryADeliveryWhoseWorkerDisappeared has a worker claim a
// Delivery and never make its Attempt, as when its process dies. Other
// workers make the Attempt once the lease runs out, 60 seconds on.
func TestWorkersRetryADeliveryWhoseWorkerDisappeared(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 200)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, rcv.URL()))
	claim(t, tw.in.store, now, 10) // The worker that disappears.

	tw.clock.set(now.Add(59 * time.Second))
	tw.runOnce(t)
	if n := rcv.count(); n != 0 {
		t.Fatalf("at now+59s: receiver got %d requests, want none while the lease runs", n)
	}
	tw.clock.set(now.Add(60 * time.Second))
	tw.runOnce(t)

	got, err := tw.in.store.Delivery(t.Context(), uuid.MustParse(deliveryID1))
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StateSucceeded {
		t.Errorf("state = %q, want %q", got.State, delivery.StateSucceeded)
	}
	want := []delivery.Attempt{{Number: 1, StartedAt: now.Add(60 * time.Second), StatusCode: 200, ResponseBody: []byte{}}}
	if diff := cmp.Diff(want, attempts(t, tw.in.store, uuid.MustParse(deliveryID1)), ignoreLatency); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
}

// TestWorkersCapASlowEndpointAcrossReplicas has two dispatcher replicas
// deliver 10 Deliveries to an Endpoint that doesn't answer and 1 to one that
// does. The slow Endpoint never has more than 4 Attempts in flight between
// them, and the other Endpoint's Delivery isn't held up behind it.
func TestWorkersCapASlowEndpointAcrossReplicas(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	slow := newGateReceiver(t)
	fast := newReceiver(t, 200)
	tw := newTestWorkers(t)
	tw.in.send(t, tw.batchTo(t, []endpointDeliveries{
		{endpointID: endpointID1, url: slow.URL(), n: 10},
		{endpointID: endpointID2, url: fast.URL(), n: 1},
	}))
	// Replica A has room for 2 Attempts, so the slow Endpoint's 4 are split
	// between the replicas.
	replicaA, replicaB := tw.newWorkers(t, 2), tw.newWorkers(t, 0)
	t.Cleanup(func() {
		slow.release()
		replicaA.Wait()
		replicaB.Wait()
	})

	for _, w := range []*delivery.Workers{replicaA, replicaB} {
		if _, err := w.RunOnce(t.Context()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	}
	slow.waitForArrivals(t, 4)
	fast.waitForArrivals(t, 1)
	// Both replicas have room, but the slow Endpoint has 4 in flight.
	for i, w := range []*delivery.Workers{replicaA, replicaB, replicaA, replicaB} {
		n, err := w.RunOnce(t.Context())
		if err != nil {
			t.Fatalf("round %d: RunOnce: %v", i+1, err)
		}
		if n != 0 {
			t.Errorf("round %d: RunOnce started %d Attempts, want none", i+1, n)
		}
	}

	if got := slow.maxInFlight(); got != 4 {
		t.Errorf("slow Endpoint had at most %d Attempts in flight, want 4", got)
	}
}

// endpointDeliveries is n Deliveries to the Endpoint at url.
type endpointDeliveries struct {
	endpointID string
	url        string
	n          int
}

// batchTo returns a Batch of one Message with the given Deliveries, each
// Endpoint's signing secrets being signingSecrets.
func (tw *testWorkers) batchTo(t *testing.T, endpoints []endpointDeliveries) *handoffv1.Batch {
	t.Helper()
	m := &handoffv1.Message{
		Id:        messageID1,
		TenantId:  tenantID,
		EventType: "invoice.paid",
		Payload:   []byte(`{"invoice": 42}`),
	}
	for _, e := range endpoints {
		endpoint := &handoffv1.Endpoint{Id: e.endpointID, Url: e.url, SigningSecrets: tw.encryptSecrets(t, e.endpointID)}
		for range e.n {
			m.Deliveries = append(m.Deliveries, &handoffv1.Delivery{Id: uuid.NewV7().String(), Endpoint: endpoint})
		}
	}
	return &handoffv1.Batch{Messages: []*handoffv1.Message{m}}
}

// encryptSecrets returns signingSecrets encrypted for the Endpoint.
func (tw *testWorkers) encryptSecrets(t *testing.T, endpointID string) []*handoffv1.SigningSecret {
	t.Helper()
	var secrets []*handoffv1.SigningSecret
	for i, s := range signingSecrets {
		ciphertext, err := tw.keys.Encrypt([]byte(s), []byte(endpointID))
		if err != nil {
			t.Fatalf("encrypt signing secret %d: %v", i, err)
		}
		secrets = append(secrets, &handoffv1.SigningSecret{Ciphertext: ciphertext})
	}
	return secrets
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
	tw := &testWorkers{in: in, clock: clock, keys: keys}
	tw.workers = tw.newWorkers(t, 0)
	return tw
}

// newWorkers returns another set of Workers on the same database, as in
// another dispatcher replica, with at most concurrency Attempts in flight (0
// for the default).
func (tw *testWorkers) newWorkers(t *testing.T, concurrency int) *delivery.Workers {
	t.Helper()
	return delivery.NewWorkers(tw.in.store, delivery.WorkersConfig{
		Keyring:        tw.keys,
		Client:         &http.Client{},
		Schedule:       pinnedSchedule(t),
		Now:            tw.clock.now,
		Concurrency:    concurrency,
		TracerProvider: tw.in.spans.Provider,
	}, slog.New(slog.DiscardHandler))
}

// TestWorkersLogEachAttempt checks the structured log line of an Attempt
// that failed with a response.
func TestWorkersLogEachAttempt(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 503)
	tw := newTestWorkers(t)
	var logs bytes.Buffer
	tw.workers = delivery.NewWorkers(tw.in.store, delivery.WorkersConfig{
		Keyring:        tw.keys,
		Client:         &http.Client{},
		Schedule:       pinnedSchedule(t),
		Now:            tw.clock.now,
		TracerProvider: tw.in.spans.Provider,
	}, slog.New(slog.NewJSONHandler(&logs, nil)))
	tw.in.send(t, tw.workerBatch(t, rcv.URL()))

	tw.runOnce(t)

	var got map[string]any
	if err := json.Unmarshal(logs.Bytes(), &got); err != nil {
		t.Fatalf("decode log line %q: %v", logs.String(), err)
	}
	if _, ok := got["latency_ms"].(float64); !ok {
		t.Errorf("latency_ms = %v, want a number", got["latency_ms"])
	}
	delete(got, "time")
	delete(got, "latency_ms")
	want := map[string]any{
		"level":       "INFO",
		"msg":         "attempt",
		"delivery_id": deliveryID1,
		"message_id":  messageID1,
		"endpoint_id": endpointID1,
		"attempt":     float64(1),
		"status_code": float64(503),
		"state":       "pending",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("log line (-want +got):\n%s", diff)
	}
}

// pinnedSchedule returns the full retry schedule with its jitter pinned to
// none, so every delay is exact.
func pinnedSchedule(t *testing.T) retry.Schedule {
	t.Helper()
	schedule, err := retry.New(1, func() float64 { return 0.5 })
	if err != nil {
		t.Fatalf("retry schedule: %v", err)
	}
	return schedule
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
				SigningSecrets: tw.encryptSecrets(t, endpointID1),
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
	arrivals

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
	r := &receiver{statuses: statuses, arrivals: newArrivals()}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

// gateReceiver is an Endpoint that holds every request until released, and
// counts how many it holds at once.
type gateReceiver struct {
	srv *httptest.Server
	arrivals
	gate    chan struct{}
	release func()

	mu       sync.Mutex
	inFlight int
	peak     int
}

func newGateReceiver(t *testing.T) *gateReceiver {
	t.Helper()
	gate := make(chan struct{})
	r := &gateReceiver{arrivals: newArrivals(), gate: gate, release: sync.OnceFunc(func() { close(gate) })}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	// Cleanups run last first: release the held requests before Close waits
	// for them.
	t.Cleanup(r.srv.Close)
	t.Cleanup(r.release)
	return r
}

func (r *gateReceiver) URL() string { return r.srv.URL + "/webhooks" }

// maxInFlight returns the most requests the receiver held at once.
func (r *gateReceiver) maxInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}

func (r *gateReceiver) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.inFlight++
	r.peak = max(r.peak, r.inFlight)
	r.mu.Unlock()
	r.arrived()
	select {
	case <-r.gate:
	case <-req.Context().Done():
	}
	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// arrivals signals each request a receiver gets.
type arrivals struct {
	ch chan struct{}
}

func newArrivals() arrivals {
	// Larger than any test sends, so a request never waits for the test.
	return arrivals{ch: make(chan struct{}, 100)}
}

func (a arrivals) arrived() { a.ch <- struct{}{} }

// waitForArrivals waits until n more requests have arrived.
func (a arrivals) waitForArrivals(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for i := range n {
		select {
		case <-a.ch:
		case <-timeout:
			t.Fatalf("%d of %d requests arrived within 10s", i, n)
		}
	}
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
	r.arrived()
	w.WriteHeader(status)
	if status >= 300 {
		fmt.Fprintf(w, "status %d", status)
	}
}
