package tenantapi_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/testing/protocmp"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
)

// payload is a JSON payload that re-encoding would change: an integer beyond
// float64's 53 bits, keys out of order, and spacing.
const payload = `{"invoice_id": 9007199254740993, "amount": 100,  "currency":"eur"}`

func TestCreateMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			var application tenantv1.CreateApplicationResponse
			tr.mustInvoke(t, a, key, createApplication("Customer A"), &application)
			applicationID := application.GetApplication().GetId()

			var created tenantv1.CreateMessageResponse
			tr.mustInvoke(t, a, key, createMessage(applicationID, "invoice.paid", payload, ""), &created)
			msg := created.GetMessage()
			if msg.GetId() == "" {
				t.Fatal("created Message has no ID")
			}
			if msg.GetCreatedAt() == nil {
				t.Error("created Message has no created_at")
			}
			want := &tenantv1.Message{
				Id:            msg.GetId(),
				ApplicationId: applicationID,
				EventType:     "invoice.paid",
				Payload:       payload,
				CreatedAt:     msg.GetCreatedAt(),
			}
			if diff := cmp.Diff(want, msg, protocmp.Transform()); diff != "" {
				t.Errorf("CreateMessage (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRepeatWithIdempotencyKeyReturnsTheOriginalMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			var application tenantv1.CreateApplicationResponse
			tr.mustInvoke(t, a, key, createApplication("Customer A"), &application)
			applicationID := application.GetApplication().GetId()
			send := func(idempotencyKey string) *tenantv1.Message {
				t.Helper()
				var resp tenantv1.CreateMessageResponse
				tr.mustInvoke(t, a, key, createMessage(applicationID, "invoice.paid", payload, idempotencyKey), &resp)
				return resp.GetMessage()
			}

			// The transports share the Tenant, so each uses keys of its own.
			original := send(tr.name + "-order-1")
			if diff := cmp.Diff(original, send(tr.name+"-order-1"), protocmp.Transform()); diff != "" {
				t.Errorf("repeat returned another Message (-original +repeat):\n%s", diff)
			}
			// Positive control: another key is another Message.
			if other := send(tr.name + "-order-2"); other.GetId() == original.GetId() {
				t.Errorf("another key returned the original Message %s", original.GetId())
			}
		})
	}
}

func TestRepeatWithADifferentRequestIsRefused(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			var application tenantv1.CreateApplicationResponse
			tr.mustInvoke(t, a, key, createApplication("Customer A"), &application)
			applicationID := application.GetApplication().GetId()
			// The transports share the Tenant, so each uses a key of its own.
			tr.mustInvoke(t, a, key, createMessage(applicationID, "invoice.paid", payload, tr.name+"-order-1"), nil)

			// REST answers 422 Unprocessable Content.
			c := createMessage(applicationID, "invoice.refunded", payload, tr.name+"-order-1")
			if code := tr.invoke(t, a, "Bearer "+key, c, nil); code != codes.FailedPrecondition {
				t.Errorf("code = %s, want FailedPrecondition", code)
			}
		})
	}
}

func TestIdempotencyKeyMustBePrintableASCII(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	applicationID := application.GetApplication().GetId()

	// Only over REST: gRPC clients refuse to send metadata that isn't
	// printable ASCII.
	tests := []struct {
		name           string
		idempotencyKey string
		wantCode       codes.Code
	}{
		{name: "non-ASCII", idempotencyKey: "café", wantCode: codes.InvalidArgument},
		// Postgres text can't hold it.
		{name: "invalid UTF-8", idempotencyKey: "caf\xe9", wantCode: codes.InvalidArgument},
		{name: "printable ASCII", idempotencyKey: "order 1/~!", wantCode: codes.OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := createMessage(applicationID, "invoice.paid", payload, tt.idempotencyKey)
			if code, body := doREST(t, a, "Bearer "+key, c); code != tt.wantCode {
				t.Errorf("code = %s, want %s: %s", code, tt.wantCode, body)
			}
		})
	}
}

func TestMessageLimits(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	applicationID := application.GetApplication().GetId()

	// jsonOfSize returns a JSON payload of exactly n bytes: a string of c.
	jsonOfSize := func(n int, c string) string {
		return `"` + strings.Repeat(c, n-2) + `"`
	}
	const maxPayload = 256 << 10

	tests := []struct {
		name     string
		call     call
		wantCode codes.Code
	}{
		{
			name:     "payload at the limit",
			call:     createMessage(applicationID, "invoice.paid", jsonOfSize(maxPayload, "a"), ""),
			wantCode: codes.OK,
		},
		{
			// The REST body escapes each "<" as the 6 bytes \u003c, as
			// encoding/json does by default, so it is 6 times the payload.
			name:     "payload at the limit, escaped in full",
			call:     createMessage(applicationID, "invoice.paid", jsonOfSize(maxPayload, "<"), ""),
			wantCode: codes.OK,
		},
		{
			// REST answers 413 Content Too Large.
			name:     "payload over the limit",
			call:     createMessage(applicationID, "invoice.paid", jsonOfSize(maxPayload+1, "a"), ""),
			wantCode: codes.ResourceExhausted,
		},
		{
			name:     "Idempotency-Key at the limit",
			call:     createMessage(applicationID, "invoice.paid", payload, strings.Repeat("k", 256)),
			wantCode: codes.OK,
		},
		{
			name:     "Idempotency-Key over the limit",
			call:     createMessage(applicationID, "invoice.paid", payload, strings.Repeat("k", 257)),
			wantCode: codes.InvalidArgument,
		},
	}
	for _, tr := range transports {
		for _, tt := range tests {
			t.Run(tr.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				if code := tr.invoke(t, a, "Bearer "+key, tt.call, nil); code != tt.wantCode {
					t.Errorf("code = %s, want %s", code, tt.wantCode)
				}
			})
		}
	}
}
