package outbox_test

import (
	"fmt"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.opentelemetry.io/otel/trace"

	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/outbox"
	"github.com/madskrogh/herald/internal/telemetry"
	"github.com/madskrogh/herald/internal/telemetry/telemetrytest"
)

// Two Tenant API calls' trace contexts, as the Messages accepted in them
// keep them.
const (
	callA = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	callB = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

// TestABatchContinuesEachMessagesTrace hands off one Batch holding Messages
// from two traces, and one stored without trace context. Each Message's
// handoff and the storing of its Deliveries join the Message's own trace,
// and the Batch, a trace of its own, links to each Message's handoff.
func TestABatchContinuesEachMessagesTrace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	env.dispatcher(t, env.subject)
	app := env.application(t)
	app.endpoint(t, "https://example.com/webhooks", nil)
	app.endpoint(t, "https://example.org/hooks", nil)
	msgA := app.messageInTrace(t, callA)
	msgB := app.messageInTrace(t, callB)
	// Stored before Messages kept their trace context.
	msgOld := app.messageInTrace(t, "")
	relay := env.relay(t, outbox.Config{})

	if n, _, err := relay.RunOnce(t.Context(), handedOff); err != nil || n != 3 {
		t.Fatalf("RunOnce = %d, %v; want 3 Messages handed off", n, err)
	}

	handoffs := byMessage(t, env.spans.Named(telemetry.SpanHandoff))
	stores := byMessage(t, env.spans.Named(telemetry.SpanStoreDeliveries))
	for _, tt := range []struct {
		msg    ingest.Message
		parent string
	}{
		{msgA, callA},
		{msgB, callB},
	} {
		if got := traceParent(handoffs[tt.msg.ID].Parent); got != tt.parent {
			t.Errorf("Message %s: handoff span's parent = %s, want the call's %s", tt.msg.ID, got, tt.parent)
		}
	}
	if old := handoffs[msgOld.ID]; old.Parent.IsValid() {
		t.Errorf("Message stored without trace context: handoff span has parent %s, want none", traceParent(old.Parent))
	}
	for _, msg := range []ingest.Message{msgA, msgB, msgOld} {
		handoff, store := handoffs[msg.ID], stores[msg.ID]
		if got, want := traceParent(store.Parent), traceParent(handoff.Context); got != want {
			t.Errorf("Message %s: store span's parent = %s, want its handoff span %s", msg.ID, got, want)
		}
		// Each Delivery keeps the store span's context, which its
		// Attempts continue.
		for _, d := range env.storedDeliveries(t, msg) {
			if got, want := d.TraceParent, traceParent(store.Context); got != want {
				t.Errorf("Message %s: Delivery's traceparent = %s, want its store span %s", msg.ID, got, want)
			}
		}
	}
	if handoffs[msgA.ID].Context.TraceID() == handoffs[msgB.ID].Context.TraceID() {
		t.Error("Messages from two traces were handed off in one trace")
	}

	batches := env.spans.Named(telemetry.SpanHandoffBatch)
	if len(batches) != 1 {
		t.Fatalf("got %d Batch spans, want 1", len(batches))
	}
	batch := batches[0]
	if batch.Parent.IsValid() {
		t.Errorf("Batch span has parent %s, want none: the Batch is a trace of its own", traceParent(batch.Parent))
	}
	wantLinks := []trace.SpanContext{handoffs[msgA.ID].Context, handoffs[msgB.ID].Context, handoffs[msgOld.ID].Context}
	if diff := cmp.Diff(wantLinks, batch.Links, cmpopts.SortSlices(func(a, b trace.SpanContext) bool {
		return a.SpanID().String() < b.SpanID().String()
	}), cmp.Comparer(trace.SpanContext.Equal)); diff != "" {
		t.Errorf("Batch span's links (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]string{"herald.batch_size": "3", "herald.outcome": "handed_off", "herald.error": ""}, batch.Attributes); diff != "" {
		t.Errorf("Batch span's attributes (-want +got):\n%s", diff)
	}

	wantHandoff := map[string]string{
		"herald.tenant_id":      app.tenantID.String(),
		"herald.application_id": app.id.String(),
		"herald.message_id":     msgA.ID.String(),
		"herald.batch_size":     "3",
		"herald.delivery_count": "2",
		"herald.outcome":        "handed_off",
		"herald.error":          "",
	}
	if diff := cmp.Diff(wantHandoff, handoffs[msgA.ID].Attributes); diff != "" {
		t.Errorf("handoff span's attributes (-want +got):\n%s", diff)
	}
	wantStore := map[string]string{
		"herald.tenant_id":      app.tenantID.String(),
		"herald.application_id": app.id.String(),
		"herald.message_id":     msgA.ID.String(),
		"herald.batch_size":     "3",
		"herald.delivery_count": "2",
		"herald.outcome":        "stored",
		"herald.error":          "",
	}
	if diff := cmp.Diff(wantStore, stores[msgA.ID].Attributes); diff != "" {
		t.Errorf("store span's attributes (-want +got):\n%s", diff)
	}
}

// TestHandoffSpansRecordEachMessagesOutcome checks the outcome of a Message
// that is parked, and of Messages in a round that fails.
func TestHandoffSpansRecordEachMessagesOutcome(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	t.Run("parked", func(t *testing.T) {
		t.Parallel()
		env := newEnv(t)
		app := env.application(t)
		app.endpoint(t, "https://example.com/webhooks", nil)
		good := app.messageInTrace(t, callA)
		poison := app.messageInTrace(t, callB)
		env.rejecting(t, "payload violates a constraint", poison.ID)
		relay := env.relay(t, outbox.Config{})

		if _, _, err := relay.RunOnce(t.Context(), handedOff); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}

		handoffs := byMessage(t, env.spans.Named(telemetry.SpanHandoff))
		want := map[uuid.UUID]outcome{
			good.ID:   {"handed_off", ""},
			poison.ID: {"parked", "payload violates a constraint"},
		}
		if diff := cmp.Diff(want, outcomes(handoffs)); diff != "" {
			t.Errorf("handoff outcomes (-want +got):\n%s", diff)
		}
	})
	t.Run("round failed", func(t *testing.T) {
		t.Parallel()
		env := newEnv(t)
		app := env.application(t)
		app.endpoint(t, "https://example.com/webhooks", nil)
		msg := app.messageInTrace(t, callA)
		// A dispatcher that stores nothing: its database is down.
		env.respond(t, env.subject, func(*handoffv1.Batch) *handoffv1.BatchReply {
			return &handoffv1.BatchReply{Committed: false, Error: "database is down"}
		})
		relay := env.relay(t, outbox.Config{})

		if _, _, err := relay.RunOnce(t.Context(), handedOff); err == nil {
			t.Fatal("RunOnce succeeded, want an error")
		}

		handoffs := byMessage(t, env.spans.Named(telemetry.SpanHandoff))
		want := map[uuid.UUID]outcome{
			msg.ID: {"failed", "hand off outbox: batch not committed: database is down"},
		}
		if diff := cmp.Diff(want, outcomes(handoffs)); diff != "" {
			t.Errorf("handoff outcomes (-want +got):\n%s", diff)
		}
		batches := env.spans.Named(telemetry.SpanHandoffBatch)
		if len(batches) != 1 {
			t.Fatalf("got %d Batch spans, want 1", len(batches))
		}
		if got := batches[0].Attributes["herald.outcome"]; got != "failed" {
			t.Errorf("Batch span's outcome = %q, want failed", got)
		}
	})
}

type outcome struct{ Outcome, Error string }

func outcomes(spans map[uuid.UUID]telemetrytest.Span) map[uuid.UUID]outcome {
	got := map[uuid.UUID]outcome{}
	for id, s := range spans {
		got[id] = outcome{s.Attributes["herald.outcome"], s.Attributes["herald.error"]}
	}
	return got
}

// byMessage returns spans by the Message they are about, and fails t if a
// Message has more than one.
func byMessage(t *testing.T, spans []telemetrytest.Span) map[uuid.UUID]telemetrytest.Span {
	t.Helper()
	m := map[uuid.UUID]telemetrytest.Span{}
	for _, s := range spans {
		id, err := uuid.Parse(s.Attributes["herald.message_id"])
		if err != nil {
			t.Fatalf("span %s: message ID: %v", s.Name, err)
		}
		if _, ok := m[id]; ok {
			t.Fatalf("Message %s has more than one %s span", id, s.Name)
		}
		m[id] = s
	}
	return m
}

// traceParent returns sc as a W3C traceparent, sampled.
func traceParent(sc trace.SpanContext) string {
	return fmt.Sprintf("00-%s-%s-01", sc.TraceID(), sc.SpanID())
}
