package scenario_test

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/scenario"
)

const (
	tenantID      = "0198a1b2-0000-7000-8000-0000000000aa"
	applicationID = "0198a1b2-0000-7000-8000-0000000000bb"
)

// spanRun returns finalRun's Observations and the spans a build that keeps
// every rule emits for them: a trace for each Message from its Tenant API
// call to every Attempt, a trace of its own for m003's call, which returned
// m001, and the Batch that handed both Messages off. Each span follows the
// span schema.
func spanRun() (scenario.Observations, []e2e.Span) {
	obs, _ := finalRun()
	at := func(ms int) time.Time { return edgeStart.Add(time.Duration(ms) * time.Millisecond) }
	span := func(trace, id, parent, name string, start int, attrs map[string]string) e2e.Span {
		service := "herald-api"
		if name == "store_deliveries" || name == "attempt" {
			service = "herald-dispatcher"
		}
		s := e2e.Span{
			TraceID: trace, SpanID: id, ParentSpanID: parent, Name: name, ServiceName: service,
			Start: at(start), End: at(start + 10), StatusCode: "Ok",
			Attributes: map[string]string{"herald.tenant_id": tenantID, "herald.error": ""},
		}
		for k, v := range attrs {
			s.Attributes[k] = v
		}
		return s
	}
	call := func(trace, id string, start int) e2e.Span {
		return span(trace, id, "", "tenant_api.call", start, map[string]string{
			"rpc.method":     "/herald.tenant.v1.MessageService/CreateMessage",
			"herald.outcome": "OK",
		})
	}
	handoff := func(trace, id, parent string, start int, message string) e2e.Span {
		return span(trace, id, parent, "handoff", start, map[string]string{
			"herald.application_id": applicationID,
			"herald.message_id":     message,
			"herald.batch_size":     "2",
			"herald.delivery_count": "2",
			"herald.outcome":        "handed_off",
		})
	}
	store := func(trace, id, parent string, start int, message string) e2e.Span {
		return span(trace, id, parent, "store_deliveries", start, map[string]string{
			"herald.application_id": applicationID,
			"herald.message_id":     message,
			"herald.batch_size":     "2",
			"herald.delivery_count": "2",
			"herald.outcome":        "stored",
		})
	}
	attempt := func(trace, id, parent string, start int, message, endpoint, deliveryID string, n, status int, outcome, err string) e2e.Span {
		s := span(trace, id, parent, "attempt", start, map[string]string{
			"herald.application_id": applicationID,
			"herald.message_id":     message,
			"herald.endpoint_id":    endpoint,
			"herald.delivery_id":    deliveryID,
			"herald.attempt":        strconv.Itoa(n),
			"herald.status_code":    strconv.Itoa(status),
			"herald.outcome":        outcome,
			"herald.latency_ms":     "35",
			"herald.lateness_ms":    "120",
			"herald.error":          err,
		})
		if err != "" {
			s.StatusCode = "Error"
		}
		return s
	}
	m1, m2 := edgeM1.String(), edgeM2.String()
	ep1, ep2 := edgeEP1.String(), edgeEP2.String()
	const answered500 = "the Endpoint answered 500 Internal Server Error"
	batch := span("tb", "b1", "", "handoff.batch", 400, map[string]string{"herald.batch_size": "2", "herald.outcome": "handed_off"})
	delete(batch.Attributes, "herald.tenant_id")
	batch.Links = []e2e.Link{{TraceID: "t1", SpanID: "h1"}, {TraceID: "t2", SpanID: "h2"}}
	return obs, []e2e.Span{
		call("t1", "c1", 0),
		handoff("t1", "h1", "c1", 400, m1),
		store("t1", "s1", "h1", 420, m1),
		attempt("t1", "a1", "s1", 450, m1, ep1, edgeD11.String(), 1, 200, "succeeded", ""),
		call("t2", "c2", 100),
		handoff("t2", "h2", "c2", 400, m2),
		store("t2", "s2", "h2", 420, m2),
		attempt("t2", "a2", "s2", 450, m2, ep1, edgeD21.String(), 1, 200, "succeeded", ""),
		attempt("t2", "a3", "s2", 450, m2, ep2, edgeD22.String(), 1, 500, "pending", answered500),
		attempt("t2", "a4", "s2", 950, m2, ep2, edgeD22.String(), 2, 500, "pending", answered500),
		attempt("t2", "a5", "s2", 1550, m2, ep2, edgeD22.String(), 3, 200, "succeeded", ""),
		call("t3", "c3", 200),
		batch,
	}
}

func TestCheckSpansPassesARunThatKeepsEveryRule(t *testing.T) {
	t.Parallel()
	obs, spans := spanRun()
	if got := scenario.CheckSpans(obs, spans); len(got) != 0 {
		t.Errorf("CheckSpans = %v, want no violations", got)
	}
}

// TestCheckSpansNamesWhatBroke breaks one rule at a time and checks that
// CheckSpans reports that rule, naming the Message, Delivery or span that
// broke it, and no other.
func TestCheckSpansNamesWhatBroke(t *testing.T) {
	t.Parallel()
	byID := func(spans []e2e.Span, id string) *e2e.Span {
		i := slices.IndexFunc(spans, func(s e2e.Span) bool { return s.SpanID == id })
		return &spans[i]
	}
	tests := []struct {
		name    string
		breakIt func(obs *scenario.Observations, spans []e2e.Span) []e2e.Span
		want    []violation
	}{
		{
			name: "a recorded Attempt with no span",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				return slices.DeleteFunc(spans, func(s e2e.Span) bool { return s.SpanID == "a4" })
			},
			want: []violation{{"trace", "m002"}},
		},
		{
			name: "an Attempt in a trace of its own",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				a := byID(spans, "a1")
				a.TraceID, a.ParentSpanID = "t9", ""
				return spans
			},
			want: []violation{{"trace", "m001"}, {"parent", "attempt span a1"}},
		},
		{
			name: "a Message that was never handed off",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "h1").Attributes["herald.outcome"] = "failed"
				byID(spans, "h1").Attributes["herald.error"] = "hand off outbox: nats: timeout"
				return spans
			},
			want: []violation{{"trace", "m001"}, {"error", "handoff span h1"}},
		},
		{
			name: "a span whose parent is missing",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "a1").ParentSpanID = "gone"
				return spans
			},
			want: []violation{{"parent", "attempt span a1"}},
		},
		{
			name: "a root that is neither a Tenant API call nor a Batch",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "s2").ParentSpanID = ""
				return spans
			},
			want: []violation{{"parent", "store_deliveries span s2"}},
		},
		{
			name: "an error at an Endpoint that always answers",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				a := byID(spans, "a1")
				a.StatusCode = "Error"
				a.Attributes["herald.error"] = "Post: context deadline exceeded"
				return spans
			},
			want: []violation{{"error", "attempt span a1"}},
		},
		{
			name: "an error status without the error attribute",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "s1").StatusCode = "Error"
				return spans
			},
			want: []violation{{"error", "store_deliveries span s1"}},
		},
		{
			name: "an Endpoint's error of another kind than its behaviour causes",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				a := byID(spans, "a3")
				a.Attributes["herald.status_code"] = "503"
				a.Attributes["herald.error"] = "the Endpoint answered 503 Service Unavailable"
				return spans
			},
			want: []violation{{"error", "attempt span a3"}},
		},
		{
			name: "a fault's error in a phase without the fault",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				a := clone(*byID(spans, "a3"), "a0")
				a.Attributes["herald.outcome"] = "cut_off"
				a.Attributes["herald.status_code"] = "0"
				a.Attributes["herald.error"] = "Post: context canceled"
				return append(spans, a)
			},
			want: []violation{{"error", "attempt span a0"}},
		},
		{
			name: "a refusal the scenario didn't ask for",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				c := byID(spans, "c3")
				c.StatusCode = "Error"
				c.Attributes["herald.outcome"] = "FailedPrecondition"
				c.Attributes["herald.error"] = "Idempotency-Key was used for a different request in the last 24 hours"
				return spans
			},
			want: []violation{{"error", "tenant_api.call span c3"}},
		},
		{
			name: "a refused call, when the scenario's refusal is for another reason",
			breakIt: func(obs *scenario.Observations, spans []e2e.Span) []e2e.Span {
				m := scenario.Message{Name: "m004", Application: "app-1", EventType: "not an event type", Payload: `{"message":"m004"}`}
				obs.Scenario.Phases[0].Messages = append(obs.Scenario.Phases[0].Messages, m)
				obs.Sent["m004"] = scenario.Sent{Status: 400}
				c := byID(spans, "c3")
				c.StatusCode = "Error"
				c.Attributes["herald.outcome"] = "FailedPrecondition"
				c.Attributes["herald.error"] = "Idempotency-Key was used for a different request in the last 24 hours"
				return spans
			},
			want: []violation{{"error", "tenant_api.call span c3"}},
		},
		{
			name: "a stalled handoff",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "h1").Start = byID(spans, "c1").End.Add(2500 * time.Millisecond)
				return spans
			},
			want: []violation{{"lag", "m001"}},
		},
		{
			name: "a late Attempt",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "a4").Attributes["herald.lateness_ms"] = "2500"
				return spans
			},
			want: []violation{{"lateness", "m002 to app-1/ep-2"}},
		},
		{
			name: "an Attempt span without its lateness",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				delete(byID(spans, "a4").Attributes, "herald.lateness_ms")
				return spans
			},
			want: []violation{{"schema", "attempt span a4"}, {"lateness", "m002 to app-1/ep-2"}},
		},
		{
			name: "an attribute the schema doesn't give the span",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "a1").Attributes["herald.endpoint_url"] = "http://host.docker.internal:50001/endpoints/1"
				return spans
			},
			want: []violation{{"schema", "attempt span a1"}},
		},
		{
			name: "an attribute of another type",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "a1").Attributes["herald.latency_ms"] = "35ms"
				return spans
			},
			want: []violation{{"schema", "attempt span a1"}},
		},
		{
			name: "an outcome the span doesn't take",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "c3").Attributes["herald.outcome"] = "ok"
				return spans
			},
			want: []violation{{"schema", "tenant_api.call span c3"}},
		},
		{
			name: "a parent the span may not have",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "a1").ParentSpanID = "h1"
				return spans
			},
			want: []violation{{"schema", "attempt span a1"}},
		},
		{
			name: "a span from another service",
			breakIt: func(_ *scenario.Observations, spans []e2e.Span) []e2e.Span {
				byID(spans, "s1").ServiceName = "herald-api"
				return spans
			},
			want: []violation{{"schema", "store_deliveries span s1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs, spans := spanRun()
			spans = tt.breakIt(&obs, spans)
			got := violations(scenario.CheckSpans(obs, spans))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("violations (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCheckSpansAllowsTheRefusalTheScenarioAsksFor checks that the Tenant
// API's refusal of a Message sent again with an Idempotency-Key and another
// request is no unexpected error.
func TestCheckSpansAllowsTheRefusalTheScenarioAsksFor(t *testing.T) {
	t.Parallel()
	obs, spans := spanRun()
	m := scenario.Message{Name: "m004", Application: "app-1", EventType: "invoice.paid", IdempotencyKey: "key-m001", Payload: `{"message":"m004"}`}
	obs.Scenario.Phases[0].Messages = append(obs.Scenario.Phases[0].Messages, m)
	obs.Sent["m004"] = scenario.Sent{Status: 422}
	refused := clone(spans[slices.IndexFunc(spans, func(s e2e.Span) bool { return s.SpanID == "c3" })], "c4")
	refused.TraceID = "t4"
	refused.StatusCode = "Error"
	refused.Attributes["herald.outcome"] = "FailedPrecondition"
	refused.Attributes["herald.error"] = "Idempotency-Key was used for a different request in the last 24 hours"
	if got := scenario.CheckSpans(obs, append(spans, refused)); len(got) != 0 {
		t.Errorf("CheckSpans = %v, want no violations", got)
	}
}

// TestCheckSpansLeavesTheEdgeToCheckEdge checks that an Attempt CheckEdge
// reports, at an Endpoint the scenario didn't create, isn't reported again
// with the spans.
func TestCheckSpansLeavesTheEdgeToCheckEdge(t *testing.T) {
	t.Parallel()
	obs, spans := spanRun()
	obs.Attempts = append(obs.Attempts, endpointtest.Attempt{EndpointURL: "http://host.docker.internal:50001/endpoints/9", ReceivedAt: edgeStart})
	if got := scenario.CheckSpans(obs, spans); len(got) != 0 {
		t.Errorf("CheckSpans = %v, want no violations", got)
	}
}

// TestCheckSpansAllowsWhatAFaultCauses adds to spanRun the errors and
// delays each fault causes, and checks that they are allowed under that
// fault and not without one.
func TestCheckSpansAllowsWhatAFaultCauses(t *testing.T) {
	t.Parallel()
	byID := func(spans []e2e.Span, id string) *e2e.Span {
		i := slices.IndexFunc(spans, func(s e2e.Span) bool { return s.SpanID == id })
		return &spans[i]
	}
	failed := func(spans []e2e.Span, id, err string) {
		s := byID(spans, id)
		s.StatusCode = "Error"
		s.Attributes["herald.outcome"] = "failed"
		s.Attributes["herald.error"] = err
	}
	tests := []struct {
		fault  scenario.Fault
		causes func(spans []e2e.Span) []e2e.Span
	}{
		{
			// The relay's first round fails and the next hands both
			// Messages off, 5 s later.
			fault: scenario.NATSPaused,
			causes: func(spans []e2e.Span) []e2e.Span {
				spans = append(spans, clone(*byID(spans, "h1"), "h0"))
				failed(spans, "h0", "hand off outbox: nats: timeout")
				byID(spans, "h1").Start = byID(spans, "c1").End.Add(5 * time.Second)
				return spans
			},
		},
		{
			// A dispatcher couldn't record an Attempt in time, and made it
			// again 2 s late.
			fault: scenario.PostgresSlowed,
			causes: func(spans []e2e.Span) []e2e.Span {
				a := clone(*byID(spans, "a1"), "a0")
				a.StatusCode = "Error"
				a.Attributes["herald.outcome"] = "not_recorded"
				a.Attributes["herald.error"] = "record attempt: context deadline exceeded"
				byID(spans, "a1").Attributes["herald.lateness_ms"] = "2000"
				return append(spans, a)
			},
		},
		{
			// A dispatcher recorded an Attempt, but its commit timed out, so
			// it couldn't tell.
			fault: scenario.PostgresSlowed,
			causes: func(spans []e2e.Span) []e2e.Span {
				a := byID(spans, "a1")
				a.StatusCode = "Error"
				a.Attributes["herald.outcome"] = "not_recorded"
				a.Attributes["herald.error"] = "record attempt: timeout: context deadline exceeded"
				return spans
			},
		},
		{
			// Shutdown cut an Attempt off.
			fault: scenario.DispatcherRestarted,
			causes: func(spans []e2e.Span) []e2e.Span {
				a := clone(*byID(spans, "a1"), "a0")
				a.StatusCode = "Error"
				a.Attributes["herald.outcome"] = "cut_off"
				a.Attributes["herald.status_code"] = "0"
				a.Attributes["herald.error"] = "Post: context canceled"
				byID(spans, "a1").Attributes["herald.lateness_ms"] = "6000"
				return append(spans, a)
			},
		},
	}
	for _, tt := range tests {
		t.Run(string(tt.fault), func(t *testing.T) {
			t.Parallel()
			obs, spans := spanRun()
			spans = tt.causes(spans)
			if got := scenario.CheckSpans(obs, spans); len(got) == 0 {
				t.Error("without the fault: CheckSpans = no violations, want some")
			}
			obs.Scenario.Phases[0].Fault = tt.fault
			if got := scenario.CheckSpans(obs, spans); len(got) != 0 {
				t.Errorf("under the fault: CheckSpans = %v, want no violations", got)
			}
		})
	}
}

// clone returns a copy of s with its own attributes and the given ID.
func clone(s e2e.Span, id string) e2e.Span {
	attrs := make(map[string]string, len(s.Attributes))
	for k, v := range s.Attributes {
		attrs[k] = v
	}
	s.SpanID, s.Attributes = id, attrs
	return s
}

// TestCheckSpansFailsOnNothingToCheck checks that every rule fails on a run
// with no spans, rather than passing on nothing.
func TestCheckSpansFailsOnNothingToCheck(t *testing.T) {
	t.Parallel()
	obs, _ := spanRun()
	got := map[string]bool{}
	for _, v := range scenario.CheckSpans(obs, nil) {
		got[v.Rule] = true
	}
	for _, rule := range []string{"trace", "parent", "schema", "error", "lag", "lateness"} {
		if !got[rule] {
			t.Errorf("no violation of %q on a run with no spans", rule)
		}
	}
}
