package delivery_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
)

// The signing secret of the Endpoint in workerBatch, and the keyring that
// encrypts it at rest.
const (
	signingSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	testKeyring   = "test:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
)

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
// to an Endpoint at url whose signing secret is signingSecret.
func (tw *testWorkers) workerBatch(t *testing.T, url string) *handoffv1.Batch {
	t.Helper()
	ciphertext, err := tw.keys.Encrypt([]byte(signingSecret), []byte(endpointID1))
	if err != nil {
		t.Fatalf("encrypt signing secret: %v", err)
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
				SigningSecrets: []*handoffv1.SigningSecret{{Ciphertext: ciphertext}},
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
