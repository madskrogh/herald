package tenantapi_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/telemetry"
	"github.com/madskrogh/herald/internal/telemetry/telemetrytest"
)

func TestCreateMessageStartsTheMessagesTrace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			// An API of its own, so that the outbox holds only this
			// Message.
			a := newAPI(t)
			key := a.newTenant(t, "Acme")
			var application tenantv1.CreateApplicationResponse
			tr.mustInvoke(t, a, key, createApplication("Customer A"), &application)
			applicationID := application.GetApplication().GetId()
			tr.mustInvoke(t, a, key, createEndpoint(applicationID, "https://example.com/webhooks", nil), nil)
			var created tenantv1.CreateMessageResponse
			tr.mustInvoke(t, a, key, createMessage(applicationID, "invoice.paid", payload, ""), &created)

			calls := callSpans(a.spans, "/herald.tenant.v1.MessageService/CreateMessage")
			if len(calls) != 1 {
				t.Fatalf("got %d CreateMessage spans, want 1", len(calls))
			}
			call := calls[0]
			if call.Parent.IsValid() {
				t.Errorf("CreateMessage span has parent %v, want none: the call starts the trace", call.Parent)
			}
			want := map[string]string{
				"rpc.method":       "/herald.tenant.v1.MessageService/CreateMessage",
				"herald.tenant_id": a.tenantID(t, key),
				"herald.outcome":   "OK",
				"herald.error":     "",
			}
			if diff := cmp.Diff(want, call.Attributes); diff != "" {
				t.Errorf("CreateMessage span attributes (-want +got):\n%s", diff)
			}

			// The Message keeps the call's context, for the relay to
			// continue the trace from.
			messages := a.outbox(t)
			if len(messages) != 1 {
				t.Fatalf("got %d Messages in the outbox, want 1", len(messages))
			}
			wantParent := fmt.Sprintf("00-%s-%s-01", call.Context.TraceID(), call.Context.SpanID())
			if got := messages[0].TraceParent; got != wantParent {
				t.Errorf("Message's traceparent = %q, want %q", got, wantParent)
			}
		})
	}
}

func TestEveryTenantAPICallHasASpan(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	tests := []struct {
		name          string
		authorization string
		app           string
		want          map[string]string
	}{
		{
			name:          "a call that fails",
			authorization: "Bearer " + key,
			app:           "0190a5e4-7b2c-7d3e-9f40-1a2b3c4d5e6f",
			want: map[string]string{
				"rpc.method":       "/herald.tenant.v1.ApplicationService/GetApplication",
				"herald.tenant_id": a.tenantID(t, key),
				"herald.outcome":   "NotFound",
				"herald.error":     "not found",
			},
		},
		{
			name:          "a call without an API key",
			authorization: "",
			app:           "0190a5e4-7b2c-7d3e-9f40-1a2b3c4d5e70",
			want: map[string]string{
				"rpc.method":       "/herald.tenant.v1.ApplicationService/GetApplication",
				"herald.tenant_id": "",
				"herald.outcome":   "Unauthenticated",
				"herald.error":     `want an API key as "authorization: Bearer <key>"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: the subtests share the API's spans, and tell
			// their own apart only by the order they end in.
			before := len(a.spans.Spans())
			if code := invokeGRPC(t, a, tt.authorization, getApplication(tt.app), nil); code == codes.OK {
				t.Fatalf("GetApplication: OK, want an error")
			}
			spans := a.spans.Spans()[before:]
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			if diff := cmp.Diff(tt.want, spans[0].Attributes); diff != "" {
				t.Errorf("span attributes (-want +got):\n%s", diff)
			}
		})
	}
}

// callSpans returns the Tenant API call spans for method.
func callSpans(r *telemetrytest.Recorder, method string) []telemetrytest.Span {
	var spans []telemetrytest.Span
	for _, s := range r.Named(telemetry.SpanTenantAPICall) {
		if s.Attributes[string(telemetry.RPCMethod)] == method {
			spans = append(spans, s)
		}
	}
	return spans
}

// tenantID returns the ID of the Tenant that key belongs to.
func (a *api) tenantID(t *testing.T, key string) string {
	t.Helper()
	tenant, err := a.store.TenantByAPIKey(t.Context(), key)
	if err != nil {
		t.Fatalf("TenantByAPIKey: %v", err)
	}
	return tenant.ID.String()
}

// outbox returns the Messages waiting in the outbox, as the relay claims
// them, and leaves them there.
func (a *api) outbox(t *testing.T) []ingest.OutboxMessage {
	t.Helper()
	var claimed []ingest.OutboxMessage
	_, err := a.store.HandOff(t.Context(), 100, time.Now(), func(_ context.Context, messages []ingest.OutboxMessage) (ingest.SendResult, error) {
		claimed = messages
		return ingest.SendResult{}, nil
	})
	if err != nil {
		t.Fatalf("HandOff: %v", err)
	}
	return claimed
}
