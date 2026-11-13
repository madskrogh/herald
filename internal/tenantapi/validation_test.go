package tenantapi_test

import (
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
)

func TestInvalidArguments(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	applicationID := application.GetApplication().GetId()

	const okURL = "https://example.com/webhooks"
	tests := []struct {
		name string
		call call
	}{
		{name: "empty Application name", call: createApplication("")},
		{name: "blank Application name", call: createApplication(" \t ")},
		{name: "Application name over 256 bytes", call: createApplication(strings.Repeat("é", 129))},
		// Postgres text can't hold a NUL byte.
		{name: "Application name with a NUL byte", call: createApplication("Customer\x00A")},
		{name: "Application ID not a UUID", call: getApplication("customer-a")},
		{name: "negative page size", call: listApplications(-1, "")},
		{name: "page token not base64", call: listApplications(0, "!!!")},
		{name: "page token not an ID", call: listApplications(0, "AAAA")},
		{name: "Endpoint under an Application ID that isn't a UUID", call: createEndpoint("customer-a", okURL, nil)},
		{name: "Endpoint URL without a scheme", call: createEndpoint(applicationID, "example.com/webhooks", nil)},
		{name: "Endpoint filter pattern with an empty token", call: createEndpoint(applicationID, okURL, []string{"invoice..paid"})},
		{name: "Endpoint filter with > before the end", call: createEndpoint(applicationID, okURL, []string{"invoice.>.paid"})},
		{name: "Endpoint ID not a UUID", call: getEndpoint(applicationID, "endpoint-1")},
		{name: "Endpoints page token not an ID", call: listEndpoints(applicationID, 0, "AAAA")},
		{name: "Message for an Application ID that isn't a UUID", call: createMessage("customer-a", "invoice.paid", `{}`, "")},
		{name: "Message Event type with an empty token", call: createMessage(applicationID, "invoice..paid", `{}`, "")},
		{name: "Message Event type over 128 bytes", call: createMessage(applicationID, strings.Repeat("a", 129), `{}`, "")},
		{name: "empty Message payload", call: createMessage(applicationID, "invoice.paid", "", "")},
		{name: "Message payload that isn't JSON", call: createMessage(applicationID, "invoice.paid", `{"invoice_id":`, "")},
		{name: "Message payload of two JSON values", call: createMessage(applicationID, "invoice.paid", `{} {}`, "")},
	}
	for _, tr := range transports {
		for _, tt := range tests {
			t.Run(tr.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				if code := tr.invoke(t, a, "Bearer "+key, tt.call, nil); code != codes.InvalidArgument {
					t.Errorf("code = %s, want InvalidArgument", code)
				}
			})
		}
	}

	// Positive control: arguments at the limits are accepted, so the
	// failures above come from crossing them.
	valid := []call{
		createApplication(strings.Repeat("é", 128)),
		createEndpoint(applicationID, okURL, []string{"invoice.*", "customer.>", "*"}),
		// Any JSON value is a payload, not only an object.
		createMessage(applicationID, strings.Repeat("a", 128), ` [1, "two"] `, ""),
	}
	for _, tr := range transports {
		for _, c := range valid {
			t.Run(tr.name+"/valid "+c.grpcMethod, func(t *testing.T) {
				t.Parallel()
				tr.mustInvoke(t, a, key, c, nil)
			})
		}
	}
}

func TestRESTRequestBodies(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var application tenantv1.CreateApplicationResponse
	setup.mustInvoke(t, a, key, createApplication("Customer A"), &application)
	endpoints := "/v1/applications/" + application.GetApplication().GetId() + "/endpoints"

	// padded returns a valid CreateApplication body of exactly n bytes.
	padded := func(n int) string {
		const head, tail = `{"name":"Customer B"`, `}`
		return head + strings.Repeat(" ", n-len(head)-len(tail)) + tail
	}
	const maxBody = 1600 << 10

	tests := []struct {
		name     string
		path     string
		body     string
		wantCode codes.Code
	}{
		{name: "at the size limit", path: "/v1/applications", body: padded(maxBody), wantCode: codes.OK},
		{name: "over the size limit", path: "/v1/applications", body: padded(maxBody + 1), wantCode: codes.InvalidArgument},
		{
			name:     "known fields",
			path:     endpoints,
			body:     `{"url":"https://example.com/a","filter":["invoice.paid"]}`,
			wantCode: codes.OK,
		},
		{
			// Dropping the misspelled field would leave an empty filter,
			// which accepts every Event type.
			name:     "unknown field",
			path:     endpoints,
			body:     `{"url":"https://example.com/a","filters":["invoice.paid"]}`,
			wantCode: codes.InvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := call{httpMethod: http.MethodPost, path: tt.path, body: tt.body}
			if code, body := doREST(t, a, "Bearer "+key, c); code != tt.wantCode {
				t.Errorf("code = %s, want %s: %s", code, tt.wantCode, body)
			}
		})
	}
}
