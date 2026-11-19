package delivery_test

import (
	"fmt"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/trace"

	"github.com/madskrogh/herald/internal/telemetry"
	"github.com/madskrogh/herald/internal/telemetry/telemetrytest"
)

// TestIntakeContinuesEachMessagesTrace stores a Batch whose first Message
// comes with the relay's trace context and Application, and whose second
// comes from a relay that sends neither.
func TestIntakeContinuesEachMessagesTrace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	const (
		handoff       = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
		applicationID = "0190a5e4-0000-7000-8000-0000000000b1"
	)
	in := newTestIntake(t.Context(), t)
	batch := testBatch()
	batch.Messages[0].Traceparent = handoff
	batch.Messages[0].ApplicationId = applicationID

	if reply := in.send(t, batch); !reply.GetCommitted() {
		t.Fatalf("Batch not committed: %s", reply.GetError())
	}

	stores := map[string]telemetrytest.Span{}
	for _, s := range in.spans.Named(telemetry.SpanStoreDeliveries) {
		stores[s.Attributes["herald.message_id"]] = s
	}
	if got := traceParent(stores[messageID1].Parent); got != handoff {
		t.Errorf("store span's parent = %s, want the handoff span %s", got, handoff)
	}
	if old := stores[messageID2]; old.Parent.IsValid() {
		t.Errorf("Message without trace context: store span has parent %s, want none", traceParent(old.Parent))
	}
	want := map[string]map[string]string{
		messageID1: {
			"herald.tenant_id":      tenantID,
			"herald.application_id": applicationID,
			"herald.message_id":     messageID1,
			"herald.batch_size":     "2",
			"herald.delivery_count": "2",
			"herald.outcome":        "stored",
			"herald.error":          "",
		},
		messageID2: {
			"herald.tenant_id":      tenantID,
			"herald.application_id": "",
			"herald.message_id":     messageID2,
			"herald.batch_size":     "2",
			"herald.delivery_count": "1",
			"herald.outcome":        "stored",
			"herald.error":          "",
		},
	}
	got := map[string]map[string]string{}
	for id, s := range stores {
		got[id] = s.Attributes
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("store spans' attributes by Message (-want +got):\n%s", diff)
	}

	// Each Delivery keeps its store span's context, for its Attempts.
	for _, msgID := range []string{messageID1, messageID2} {
		deliveries, err := in.store.MessageDeliveries(t.Context(), uuid.MustParse(msgID))
		if err != nil {
			t.Fatalf("MessageDeliveries: %v", err)
		}
		for _, d := range deliveries {
			if want := traceParent(stores[msgID].Context); d.TraceParent != want {
				t.Errorf("Delivery %s: traceparent = %q, want its store span %s", d.ID, d.TraceParent, want)
			}
		}
	}
}

// traceParent returns sc as a W3C traceparent, sampled.
func traceParent(sc trace.SpanContext) string {
	return fmt.Sprintf("00-%s-%s-01", sc.TraceID(), sc.SpanID())
}
