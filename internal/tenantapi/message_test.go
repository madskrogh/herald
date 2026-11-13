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

			original := send("order-1")
			if diff := cmp.Diff(original, send("order-1"), protocmp.Transform()); diff != "" {
				t.Errorf("repeat returned another Message (-original +repeat):\n%s", diff)
			}
			// Positive control: another key is another Message.
			if other := send("order-2"); other.GetId() == original.GetId() {
				t.Errorf("another key returned the original Message %s", original.GetId())
			}
		})
	}
}

func TestIdempotencyKeyLength(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	applicationID := application.GetApplication().GetId()

	tests := []struct {
		name           string
		idempotencyKey string
		wantCode       codes.Code
	}{
		{name: "at the limit", idempotencyKey: strings.Repeat("k", 256), wantCode: codes.OK},
		{name: "over the limit", idempotencyKey: strings.Repeat("k", 257), wantCode: codes.InvalidArgument},
	}
	for _, tr := range transports {
		for _, tt := range tests {
			t.Run(tr.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				c := createMessage(applicationID, "invoice.paid", payload, tt.idempotencyKey)
				if code := tr.invoke(t, a, "Bearer "+key, c, nil); code != tt.wantCode {
					t.Errorf("code = %s, want %s", code, tt.wantCode)
				}
			})
		}
	}
}

func TestPayloadSizeLimit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	applicationID := application.GetApplication().GetId()

	// jsonOfSize returns a JSON payload of exactly n bytes: a string.
	jsonOfSize := func(n int) string {
		return `"` + strings.Repeat("a", n-2) + `"`
	}
	const maxPayload = 256 << 10

	tests := []struct {
		name     string
		payload  string
		wantCode codes.Code
	}{
		{name: "at the limit", payload: jsonOfSize(maxPayload), wantCode: codes.OK},
		// REST answers 413 Content Too Large.
		{name: "over the limit", payload: jsonOfSize(maxPayload + 1), wantCode: codes.ResourceExhausted},
	}
	for _, tr := range transports {
		for _, tt := range tests {
			t.Run(tr.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				c := createMessage(applicationID, "invoice.paid", tt.payload, "")
				if code := tr.invoke(t, a, "Bearer "+key, c, nil); code != tt.wantCode {
					t.Errorf("code = %s, want %s", code, tt.wantCode)
				}
			})
		}
	}
}
