package tenantapi_test

import (
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
	var app tenantv1.CreateApplicationResponse
	transports[0].mustInvoke(t, a, key, createApplication("Customer A"), &app)
	appID := app.GetApplication().GetId()

	const okURL = "https://example.com/webhooks"
	tests := []struct {
		name string
		call call
	}{
		{name: "empty Application name", call: createApplication("")},
		{name: "blank Application name", call: createApplication(" \t ")},
		{name: "Application name over 256 bytes", call: createApplication(strings.Repeat("é", 129))},
		{name: "Application ID not a UUID", call: getApplication("customer-a")},
		{name: "negative page size", call: listApplications(-1, "")},
		{name: "page token not base64", call: listApplications(0, "!!!")},
		{name: "page token not an ID", call: listApplications(0, "AAAA")},
		{name: "Endpoint under an Application ID that isn't a UUID", call: createEndpoint("customer-a", okURL, nil)},
		{name: "Endpoint URL without a scheme", call: createEndpoint(appID, "example.com/webhooks", nil)},
		{name: "Endpoint filter pattern with an empty token", call: createEndpoint(appID, okURL, []string{"invoice..paid"})},
		{name: "Endpoint filter with > before the end", call: createEndpoint(appID, okURL, []string{"invoice.>.paid"})},
		{name: "Endpoint ID not a UUID", call: getEndpoint(appID, "endpoint-1")},
		{name: "Endpoints page token not an ID", call: listEndpoints(appID, 0, "AAAA")},
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
		createEndpoint(appID, okURL, []string{"invoice.*", "customer.>", "*"}),
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
