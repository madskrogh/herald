package tenantapi_test

import (
	"testing"

	"google.golang.org/grpc/codes"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
)

func TestOtherTenantsResourcesAreNotFound(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	owner := a.newTenant(t, "Acme")
	other := a.newTenant(t, "Globex")
	var app tenantv1.CreateApplicationResponse
	transports[0].mustInvoke(t, a, owner, createApplication("Customer A"), &app)
	appID := app.GetApplication().GetId()
	var ep tenantv1.CreateEndpointResponse
	transports[0].mustInvoke(t, a, owner, createEndpoint(appID, "https://example.com/webhooks", nil), &ep)

	// Every operation on a resource that belongs to owner.
	operations := []call{
		getApplication(appID),
		createEndpoint(appID, "https://example.com/other", nil),
		getEndpoint(appID, ep.GetEndpoint().GetId()),
		listEndpoints(appID, 0, ""),
	}
	for _, tr := range transports {
		for _, op := range operations {
			t.Run(tr.name+"/"+op.grpcMethod, func(t *testing.T) {
				t.Parallel()
				if code := tr.invoke(t, a, "Bearer "+other, op, nil); code != codes.NotFound {
					t.Errorf("another Tenant's call: code = %s, want NotFound", code)
				}
				// Positive control: the owner's call succeeds, so the failure
				// comes from who is asking.
				tr.mustInvoke(t, a, owner, op, nil)
			})
		}
	}
}
