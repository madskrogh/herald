package tenantapi_test

import (
	"testing"

	"google.golang.org/grpc/codes"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
)

func TestCallsNeedAValidAPIKey(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var app tenantv1.CreateApplicationResponse
	transports[0].mustInvoke(t, a, key, createApplication("Customer A"), &app)
	appID := app.GetApplication().GetId()
	var ep tenantv1.CreateEndpointResponse
	transports[0].mustInvoke(t, a, key, createEndpoint(appID, "https://example.com/webhooks", nil), &ep)

	// Every operation, with arguments that would succeed with a valid key, so
	// that only the key can make them fail.
	operations := []call{
		createApplication("Customer B"),
		getApplication(appID),
		listApplications(0, ""),
		createEndpoint(appID, "https://example.com/other", nil),
		getEndpoint(appID, ep.GetEndpoint().GetId()),
		listEndpoints(appID, 0, ""),
	}
	authorizations := []struct {
		name  string
		value string
	}{
		{name: "none", value: ""},
		{name: "unknown key", value: "Bearer herald_AAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{name: "key without scheme", value: key},
		{name: "other scheme", value: "Basic " + key},
		{name: "scheme without key", value: "Bearer"},
		{name: "scheme and empty key", value: "Bearer "},
	}
	for _, tr := range transports {
		for _, op := range operations {
			for _, auth := range authorizations {
				t.Run(tr.name+"/"+op.grpcMethod+"/"+auth.name, func(t *testing.T) {
					t.Parallel()
					if code := tr.invoke(t, a, auth.value, op, nil); code != codes.Unauthenticated {
						t.Errorf("code = %s, want Unauthenticated", code)
					}
				})
			}
		}
	}

	// Positive control: the same calls succeed with the valid key, so the
	// failures above come from the key alone.
	for _, tr := range transports {
		for _, op := range operations {
			t.Run(tr.name+"/"+op.grpcMethod+"/valid key", func(t *testing.T) {
				t.Parallel()
				tr.mustInvoke(t, a, key, op, nil)
			})
		}
	}
}
