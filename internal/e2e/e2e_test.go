package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
)

// stack is the running stack under test, or nil when none is configured and
// every test skips.
var stack *e2e.Stack

func TestMain(m *testing.M) {
	cfg, ok, err := e2e.ConfigFromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	if ok {
		stack, err = e2e.Connect(context.Background(), cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "e2e:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if stack != nil {
		stack.Close()
	}
	os.Exit(code)
}

// waitTimeout bounds each test's waits. With the local stack's retry scale
// (HERALD_RETRY_SCALE in compose.yaml), the whole retry schedule takes about
// 80 seconds, 88 at most with jitter, and every other scenario here a few.
const waitTimeout = 2 * time.Minute

// setup skips the test unless a stack is configured, and returns a context
// that bounds its waits and a Tenant of its own with one Application.
func setup(t *testing.T) (context.Context, *e2e.Tenant, string) {
	t.Helper()
	ctx := stackContext(t)
	tenant, err := stack.NewTenant(ctx, t.Name())
	if err != nil {
		t.Fatalf("NewTenant: %v", err)
	}
	return ctx, tenant, createApplication(ctx, t, tenant)
}

// stackContext skips the test unless a stack is configured, and returns a
// context that bounds its waits.
func stackContext(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("end-to-end")
	}
	if stack == nil {
		t.Skip("no stack: run with make e2e")
	}
	ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
	t.Cleanup(cancel)
	return ctx
}

// TestSpanIsStoredInClickHouse sends a span to the stack's Collector and
// reads it back from ClickHouse, in the exporter's table that the telemetry
// checks query.
func TestSpanIsStoredInClickHouse(t *testing.T) {
	t.Parallel()
	ctx := stackContext(t)
	// The trace ID is the test's own, so the span is found among any others.
	span := e2e.Span{
		TraceID:     randomHex(16),
		SpanID:      randomHex(8),
		Name:        "e2e.check",
		ServiceName: "herald-e2e",
		Attributes:  map[string]string{"herald.test": "TestSpanIsStoredInClickHouse"},
	}
	if err := stack.SendSpan(ctx, span); err != nil {
		t.Fatalf("SendSpan: %v", err)
	}
	got, err := stack.WaitSpans(ctx, span.TraceID)
	if err != nil {
		t.Fatalf("WaitSpans: %v", err)
	}
	span.ResourceAttributes = map[string]string{"service.name": "herald-e2e"}
	if diff := cmp.Diff([]e2e.Span{span}, got); diff != "" {
		t.Errorf("spans in trace %s (-want +got):\n%s", span.TraceID, diff)
	}
}

// randomHex returns n random bytes in hex, as OTLP writes trace and span IDs.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b) // Never returns an error; it crashes the program instead.
	return hex.EncodeToString(b)
}

func TestMessageIsDeliveredSigned(t *testing.T) {
	t.Parallel()
	ctx, tenant, app := setup(t)
	ep := stack.Endpoints.NewEndpoint(endpointtest.Succeed())
	createEndpoint(ctx, t, tenant, app, ep)

	payload := `{"invoice": 42, "amount": 12345678901234567890}`
	messageID := sendMessage(ctx, t, tenant, app, payload, "")

	attempts := waitAttempts(ctx, t, ep, 1)
	checkSigned(t, attempts, messageID, payload)
	waitOneDelivery(ctx, t, messageID, delivery.StateSucceeded)
}

func TestFailedAttemptsAreRetriedWithTheSameWebhookID(t *testing.T) {
	t.Parallel()
	ctx, tenant, app := setup(t)
	ep := stack.Endpoints.NewEndpoint(endpointtest.FailTimes(2))
	createEndpoint(ctx, t, tenant, app, ep)

	payload := `{"invoice": 42}`
	messageID := sendMessage(ctx, t, tenant, app, payload, "")

	waitOneDelivery(ctx, t, messageID, delivery.StateSucceeded)
	// The Delivery has succeeded, so no Attempt comes after these.
	attempts := waitAttempts(ctx, t, ep, 3)
	if len(attempts) != 3 {
		t.Errorf("Endpoint received %d Attempts, want 3: two failed, then one succeeded", len(attempts))
	}
	checkSigned(t, attempts, messageID, payload)
}

// TestAttemptThatTimesOutIsRetried leaves an Endpoint's first Attempt
// unanswered. The dispatcher must give up on it after 15 seconds, record it
// and retry, once: a lease that ran out while the Attempt hung would let
// another worker make a third.
func TestAttemptThatTimesOutIsRetried(t *testing.T) {
	t.Parallel()
	ctx, tenant, app := setup(t)
	ep := stack.Endpoints.NewEndpoint(endpointtest.TimeOutTimes(1))
	createEndpoint(ctx, t, tenant, app, ep)

	payload := `{"invoice": 42}`
	messageID := sendMessage(ctx, t, tenant, app, payload, "")

	waitOneDelivery(ctx, t, messageID, delivery.StateSucceeded)
	// The Delivery has succeeded, so no Attempt comes after these.
	attempts := waitAttempts(ctx, t, ep, 2)
	if len(attempts) != 2 {
		t.Fatalf("Endpoint received %d Attempts, want 2: one timed out, then one succeeded", len(attempts))
	}
	checkSigned(t, attempts, messageID, payload)
	// The retry comes only once the dispatcher has given up, not sooner.
	if gap := attempts[1].ReceivedAt.Sub(attempts[0].ReceivedAt); gap < 15*time.Second {
		t.Errorf("second Attempt %s after the first, want at least the 15 s timeout", gap)
	}
}

func TestDeliveryFailsWhenTheRetryScheduleRunsOut(t *testing.T) {
	t.Parallel()
	ctx, tenant, app := setup(t)
	// Fails every Attempt the schedule has, and would accept a ninth.
	ep := stack.Endpoints.NewEndpoint(endpointtest.FailTimes(8))
	createEndpoint(ctx, t, tenant, app, ep)

	payload := `{"invoice": 42}`
	messageID := sendMessage(ctx, t, tenant, app, payload, "")

	waitOneDelivery(ctx, t, messageID, delivery.StateFailed)
	// The Delivery has failed, so no Attempt comes after these.
	attempts := waitAttempts(ctx, t, ep, 8)
	if len(attempts) != 8 {
		t.Errorf("Endpoint received %d Attempts, want the schedule's 8", len(attempts))
	}
	checkSigned(t, attempts, messageID, payload)
}

func TestRepeatedIdempotencyKeyMakesOneDelivery(t *testing.T) {
	t.Parallel()
	ctx, tenant, app := setup(t)
	ep := stack.Endpoints.NewEndpoint(endpointtest.Succeed())
	createEndpoint(ctx, t, tenant, app, ep)

	payload := `{"invoice": 42}`
	first := sendMessage(ctx, t, tenant, app, payload, "invoice-42")
	repeat := sendMessage(ctx, t, tenant, app, payload, "invoice-42")
	// The positive control: the same request under another key is another
	// Message, so the Endpoint can receive two.
	other := sendMessage(ctx, t, tenant, app, payload, "invoice-42-again")

	if repeat != first {
		t.Errorf("repeated Idempotency-Key: Message ID = %s, want the first's, %s", repeat, first)
	}
	if other == first {
		t.Errorf("another Idempotency-Key: Message ID = %s, the same as the first's", other)
	}
	waitOneDelivery(ctx, t, first, delivery.StateSucceeded)
	waitOneDelivery(ctx, t, other, delivery.StateSucceeded)
	// Both Messages' Deliveries have succeeded, so no Attempt comes after
	// these.
	attempts := waitAttempts(ctx, t, ep, 2)
	got := map[string]int{}
	for _, a := range attempts {
		got[a.WebhookID]++
	}
	if diff := cmp.Diff(map[string]int{first: 1, other: 1}, got); diff != "" {
		t.Errorf("Attempts per webhook-id (-want +got):\n%s", diff)
	}
}

// TestSlowEndpointDoesNotDelayAnother holds an Endpoint's Attempts open and
// checks that another Endpoint's Delivery succeeds meanwhile. The slow
// Endpoint has more Deliveries than a dispatcher has workers, so only its cap
// of 4 Attempts in flight keeps it from holding them all.
func TestSlowEndpointDoesNotDelayAnother(t *testing.T) {
	t.Parallel()
	ctx, tenant, slowApp := setup(t)
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	// Releases the held Attempts if the test fails before it does, so they
	// don't hold workers until their timeout.
	t.Cleanup(releaseOnce)
	slow := stack.Endpoints.NewEndpoint(endpointtest.Hold(release))
	createEndpoint(ctx, t, tenant, slowApp, slow)
	fastApp := createApplication(ctx, t, tenant)
	fast := stack.Endpoints.NewEndpoint(endpointtest.Succeed())
	createEndpoint(ctx, t, tenant, fastApp, fast)

	// One more than the 32 workers a dispatcher runs.
	var slowMessages []string
	for i := range 33 {
		slowMessages = append(slowMessages, sendMessage(ctx, t, tenant, slowApp, fmt.Sprintf(`{"invoice": %d}`, i), ""))
	}
	waitAttempts(ctx, t, slow, 4)
	fastMessage := sendMessage(ctx, t, tenant, fastApp, `{"invoice": 99}`, "")

	// Held Attempts time out after 15 seconds and free their workers, so the
	// fast Delivery would get through eventually even if the slow Endpoint
	// held every worker. It must succeed well before then.
	fastCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	waitOneDelivery(fastCtx, t, fastMessage, delivery.StateSucceeded)
	// Nothing has released the slow Endpoint's Attempts, so the fast Delivery
	// didn't wait for any of them, and the cap held the slow Endpoint to 4.
	if got := len(waitAttempts(ctx, t, slow, 0)); got != 4 {
		t.Errorf("slow Endpoint received %d Attempts while held, want 4", got)
	}
	releaseOnce()
	for _, m := range slowMessages {
		waitOneDelivery(ctx, t, m, delivery.StateSucceeded)
	}
}

func createApplication(ctx context.Context, t *testing.T, tenant *e2e.Tenant) string {
	t.Helper()
	id, err := tenant.CreateApplication(ctx, "Acme")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	return id
}

func createEndpoint(ctx context.Context, t *testing.T, tenant *e2e.Tenant, app string, ep *endpointtest.Endpoint) {
	t.Helper()
	if _, err := tenant.CreateEndpoint(ctx, app, ep); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
}

func sendMessage(ctx context.Context, t *testing.T, tenant *e2e.Tenant, app, payload, idempotencyKey string) string {
	t.Helper()
	id, err := tenant.SendMessage(ctx, app, "invoice.paid", payload, idempotencyKey)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	return id
}

func waitAttempts(ctx context.Context, t *testing.T, ep *endpointtest.Endpoint, n int) []endpointtest.Attempt {
	t.Helper()
	attempts, err := ep.WaitAttempts(ctx, n)
	if err != nil {
		t.Fatalf("WaitAttempts: %v", err)
	}
	return attempts
}

// checkSigned checks that every Attempt carries the Message's ID as its
// webhook-id and its payload byte for byte, signed with the Endpoint's
// secret.
func checkSigned(t *testing.T, attempts []endpointtest.Attempt, messageID, payload string) {
	t.Helper()
	for _, a := range attempts {
		if a.WebhookID != messageID {
			t.Errorf("Attempt %d: webhook-id = %q, want the Message ID %q", a.Number, a.WebhookID, messageID)
		}
		if string(a.Body) != payload {
			t.Errorf("Attempt %d: body = %s, want %s", a.Number, a.Body, payload)
		}
		if a.SignatureErr != nil {
			t.Errorf("Attempt %d: signature: %v", a.Number, a.SignatureErr)
		}
	}
}

// waitOneDelivery waits until the Message's Deliveries have finished and
// checks that it has one, in state want.
func waitOneDelivery(ctx context.Context, t *testing.T, messageID string, want delivery.State) {
	t.Helper()
	deliveries, err := stack.WaitDeliveries(ctx, messageID)
	if err != nil {
		t.Fatalf("WaitDeliveries: %v", err)
	}
	var got []delivery.State
	for _, d := range deliveries {
		got = append(got, d.State)
	}
	if diff := cmp.Diff([]delivery.State{want}, got); diff != "" {
		t.Errorf("Message %s: Delivery states (-want +got):\n%s", messageID, diff)
	}
}
