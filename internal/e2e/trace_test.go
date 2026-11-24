package e2e_test

import (
	"cmp"
	"slices"
	"strconv"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
)

// TestMessageIsOneTraceFromTheAPIToEveryAttempt sends a Message to two
// Endpoints, one of which fails twice before it succeeds, and reads the
// Message's trace back from ClickHouse: the Tenant API call, the handoff,
// the storing of its Deliveries, and every Attempt of both, retries
// included, in one tree.
func TestMessageIsOneTraceFromTheAPIToEveryAttempt(t *testing.T) {
	t.Parallel()
	ctx, tenant, app := setup(t)
	steady := stack.Endpoints.NewEndpoint(endpointtest.Succeed())
	flaky := stack.Endpoints.NewEndpoint(endpointtest.FailTimes(2))
	steadyID, err := tenant.CreateEndpoint(ctx, app, steady)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	flakyID, err := tenant.CreateEndpoint(ctx, app, flaky)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	messageID := sendMessage(ctx, t, tenant, app, `{"invoice": 42}`, "")
	deliveries, err := stack.WaitDeliveries(ctx, messageID)
	if err != nil {
		t.Fatalf("WaitDeliveries: %v", err)
	}
	for _, d := range deliveries {
		if d.State != delivery.StateSucceeded {
			t.Fatalf("Delivery %s is %s, want succeeded", d.ID, d.State)
		}
	}

	// The call, the handoff, the store, and four Attempts.
	const wantSpans = 7
	spans, err := stack.WaitMessageTrace(ctx, messageID, wantSpans)
	if err != nil {
		t.Fatalf("WaitMessageTrace: %v", err)
	}
	if len(spans) != wantSpans {
		t.Fatalf("Message's trace has %d spans, want %d: %+v", len(spans), wantSpans, spans)
	}
	problems, err := e2e.SchemaProblems(spans)
	if err != nil {
		t.Fatalf("SchemaProblems: %v", err)
	}
	for _, s := range spans {
		for _, err := range problems[s.SpanID] {
			t.Errorf("span schema: %v", err)
		}
		for _, key := range []string{"service.version", "herald.run_id"} {
			if _, ok := s.ResourceAttributes[key]; !ok {
				t.Errorf("span %s: no resource attribute %s", s.Name, key)
			}
		}
	}

	byName := map[string][]e2e.Span{}
	for _, s := range spans {
		byName[s.Name] = append(byName[s.Name], s)
	}
	call := only(t, byName, "tenant_api.call")
	handoff := only(t, byName, "handoff")
	store := only(t, byName, "store_deliveries")
	if call.ParentSpanID != "" {
		t.Errorf("Tenant API call span has parent %s, want none", call.ParentSpanID)
	}
	if got := call.Attributes["rpc.method"]; got != "/herald.tenant.v1.MessageService/CreateMessage" {
		t.Errorf("root span's method = %s, want CreateMessage", got)
	}
	parents := []struct {
		child, parent e2e.Span
	}{
		{handoff, call},
		{store, handoff},
	}
	for _, p := range parents {
		if p.child.ParentSpanID != p.parent.SpanID {
			t.Errorf("%s span's parent = %s, want the %s span %s", p.child.Name, p.child.ParentSpanID, p.parent.Name, p.parent.SpanID)
		}
	}
	services := map[string]string{
		call.Name:    "herald-api",
		handoff.Name: "herald-api",
		store.Name:   "herald-dispatcher",
	}
	for _, s := range []e2e.Span{call, handoff, store} {
		if s.ServiceName != services[s.Name] {
			t.Errorf("%s span from %s, want %s", s.Name, s.ServiceName, services[s.Name])
		}
	}

	// Every Attempt is a child of the store span, a retry as much as the
	// first.
	attempts := byName["attempt"]
	slices.SortFunc(attempts, func(a, b e2e.Span) int {
		return cmp.Or(
			cmp.Compare(a.Attributes["herald.endpoint_id"], b.Attributes["herald.endpoint_id"]),
			cmp.Compare(attemptNumber(t, a), attemptNumber(t, b)),
		)
	})
	type attempt struct{ Endpoint, Number, Outcome, Parent, Service string }
	var got []attempt
	for _, a := range attempts {
		got = append(got, attempt{a.Attributes["herald.endpoint_id"], a.Attributes["herald.attempt"], a.Attributes["herald.outcome"], a.ParentSpanID, a.ServiceName})
	}
	want := []attempt{
		{steadyID, "1", "succeeded", store.SpanID, "herald-dispatcher"},
		{flakyID, "1", "pending", store.SpanID, "herald-dispatcher"},
		{flakyID, "2", "pending", store.SpanID, "herald-dispatcher"},
		{flakyID, "3", "succeeded", store.SpanID, "herald-dispatcher"},
	}
	slices.SortStableFunc(want, func(a, b attempt) int { return cmp.Compare(a.Endpoint, b.Endpoint) })
	if diff := gocmp.Diff(want, got); diff != "" {
		t.Errorf("Attempt spans (-want +got):\n%s", diff)
	}

	// The Batch that handed the Message off is a trace of its own, linked to
	// the Message's handoff.
	batches, err := stack.WaitBatchesLinkedTo(ctx, handoff.SpanID)
	if err != nil {
		t.Fatalf("WaitBatchesLinkedTo: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("got %d Batch spans linked to the handoff, want 1", len(batches))
	}
	if batches[0].Name != "handoff.batch" || batches[0].TraceID == call.TraceID {
		t.Errorf("span linked to the handoff is %s in trace %s, want a %s span in a trace of its own", batches[0].Name, batches[0].TraceID, "handoff.batch")
	}
}

// only returns the one span named name, and fails t unless there is one.
func only(t *testing.T, byName map[string][]e2e.Span, name string) e2e.Span {
	t.Helper()
	if len(byName[name]) != 1 {
		t.Fatalf("got %d %s spans, want 1", len(byName[name]), name)
	}
	return byName[name][0]
}

func attemptNumber(t *testing.T, s e2e.Span) int {
	t.Helper()
	n, err := strconv.Atoi(s.Attributes["herald.attempt"])
	if err != nil {
		t.Fatalf("Attempt span's number %q: %v", s.Attributes["herald.attempt"], err)
	}
	return n
}
