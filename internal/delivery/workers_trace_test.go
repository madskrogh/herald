package delivery_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/madskrogh/herald/internal/telemetry"
	"github.com/madskrogh/herald/internal/telemetry/telemetrytest"
)

// TestEveryAttemptJoinsTheMessagesTrace makes two Attempts for a Delivery, the
// second by Workers started afresh, as after a dispatcher restart. Both are
// children of the span that stored the Delivery.
func TestEveryAttemptJoinsTheMessagesTrace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	const (
		handoff       = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
		applicationID = "0190a5e4-0000-7000-8000-0000000000b1"
	)
	rcv := newReceiver(t, 500, 204)
	tw := newTestWorkers(t)
	batch := tw.workerBatch(t, rcv.URL())
	batch.Messages[0].Traceparent = handoff
	batch.Messages[0].ApplicationId = applicationID
	tw.in.send(t, batch)

	tw.runOnce(t)
	tw.workers = tw.newWorkers(t, 0)
	tw.clock.set(now.Add(5 * time.Second))
	tw.runOnce(t)

	stores := tw.in.spans.Named(telemetry.SpanStoreDeliveries)
	if len(stores) != 1 {
		t.Fatalf("got %d store spans, want 1", len(stores))
	}
	attempts := tw.in.spans.Named(telemetry.SpanAttempt)
	if len(attempts) != 2 {
		t.Fatalf("got %d Attempt spans, want 2", len(attempts))
	}
	for i, a := range attempts {
		if got, want := traceParent(a.Parent), traceParent(stores[0].Context); got != want {
			t.Errorf("Attempt %d: parent = %s, want the store span %s", i+1, got, want)
		}
	}
	if got := attempts[0].Context.TraceID().String(); got != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("Attempt in trace %s, want the Message's trace 0af7651916cd43dd8448eb211c80319c", got)
	}

	want := []map[string]string{
		{
			"herald.tenant_id":      tenantID,
			"herald.application_id": applicationID,
			"herald.endpoint_id":    endpointID1,
			"herald.message_id":     messageID1,
			"herald.delivery_id":    deliveryID1,
			"herald.attempt":        "1",
			"herald.lateness_ms":    "0",
			"herald.outcome":        "pending",
			"herald.status_code":    "500",
			// An Attempt that isn't a success says why, whether it got a
			// response or not.
			"herald.error": "the Endpoint answered 500 Internal Server Error",
		},
		{
			"herald.tenant_id":      tenantID,
			"herald.application_id": applicationID,
			"herald.endpoint_id":    endpointID1,
			"herald.message_id":     messageID1,
			"herald.delivery_id":    deliveryID1,
			"herald.attempt":        "2",
			"herald.lateness_ms":    "0",
			"herald.outcome":        "succeeded",
			"herald.status_code":    "204",
			"herald.error":          "",
		},
	}
	if diff := cmp.Diff(want, withoutLatency(t, attempts)); diff != "" {
		t.Errorf("Attempt spans' attributes (-want +got):\n%s", diff)
	}
}

// TestAttemptsOfADeliveryWithoutTraceContextEachStartATrace makes two
// Attempts for a Delivery stored without trace context, as before Deliveries
// kept it.
func TestAttemptsOfADeliveryWithoutTraceContextEachStartATrace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 500, 204)
	tw := newTestWorkers(t)
	// An intake whose spans have no context stores none with the Deliveries.
	untraced := newTestIntakeOn(t.Context(), t, tw.in.dbURL, noop.NewTracerProvider())
	untraced.send(t, tw.workerBatch(t, rcv.URL()))

	tw.runOnce(t)
	tw.clock.set(now.Add(5 * time.Second))
	tw.runOnce(t)

	attempts := tw.in.spans.Named(telemetry.SpanAttempt)
	if len(attempts) != 2 {
		t.Fatalf("got %d Attempt spans, want 2", len(attempts))
	}
	for i, a := range attempts {
		if a.Parent.IsValid() {
			t.Errorf("Attempt %d: parent = %s, want none", i+1, traceParent(a.Parent))
		}
	}
	if attempts[0].Context.TraceID() == attempts[1].Context.TraceID() {
		t.Error("both Attempts are in one trace, want a trace each")
	}
	// The control for the checks above: the Attempts were made.
	if got := rcv.count(); got != 2 {
		t.Errorf("Endpoint received %d requests, want 2", got)
	}
}

// TestAttemptSpanLeavesOutTheEndpointURL makes an Attempt that gets no
// response, to an Endpoint whose URL holds a token. The HTTP client's error
// names the URL, and the span's error must not.
func TestAttemptSpanLeavesOutTheEndpointURL(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	// Nothing listens there any more, so the connection is refused.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	const token = "tok_9f2c41d7"
	url := gone.URL + "/webhooks?token=" + token
	tw := newTestWorkers(t)
	tw.in.send(t, tw.workerBatch(t, url))

	tw.runOnce(t)

	// The control for the check below: the Attempt's own error, which the
	// Tenant sees, does name the URL.
	recorded := attempts(t, tw.in.store, uuid.MustParse(deliveryID1))
	if len(recorded) != 1 || !strings.Contains(recorded[0].Error, token) {
		t.Fatalf("recorded Attempts %+v, want one whose error names the URL", recorded)
	}
	spans := tw.in.spans.Named(telemetry.SpanAttempt)
	if len(spans) != 1 {
		t.Fatalf("got %d Attempt spans, want 1", len(spans))
	}
	spanErr := spans[0].Attributes["herald.error"]
	if spanErr == "" {
		t.Error("Attempt span has no error, want why the connection failed")
	}
	if strings.Contains(spanErr, token) {
		t.Errorf("Attempt span's error %q holds the Endpoint URL's token", spanErr)
	}
}

// TestAttemptSpanSaysHowLateTheAttemptStarted makes a Delivery's first
// Attempt 2 s after it fell due, and its second 3 s after the retry
// schedule's 5 s delay.
func TestAttemptSpanSaysHowLateTheAttemptStarted(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	rcv := newReceiver(t, 500, 204)
	tw := newTestWorkers(t)
	// Stored, and due, at now.
	tw.in.send(t, tw.workerBatch(t, rcv.URL()))

	tw.clock.set(now.Add(2 * time.Second))
	tw.runOnce(t)
	tw.clock.set(now.Add(10 * time.Second))
	tw.runOnce(t)

	var got []string
	for _, a := range tw.in.spans.Named(telemetry.SpanAttempt) {
		got = append(got, a.Attributes["herald.lateness_ms"])
	}
	if diff := cmp.Diff([]string{"2000", "3000"}, got); diff != "" {
		t.Errorf("Attempt spans' herald.lateness_ms (-want +got):\n%s", diff)
	}
}

// withoutLatency returns the spans' attributes without the latency, which
// is real time even under the fake clock, after checking it is a number.
func withoutLatency(t *testing.T, spans []telemetrytest.Span) []map[string]string {
	t.Helper()
	var attrs []map[string]string
	for _, s := range spans {
		a := map[string]string{}
		for k, v := range s.Attributes {
			a[k] = v
		}
		if _, err := strconv.Atoi(a["herald.latency_ms"]); err != nil {
			t.Errorf("herald.latency_ms = %q, want a number", a["herald.latency_ms"])
		}
		delete(a, "herald.latency_ms")
		attrs = append(attrs, a)
	}
	return attrs
}
