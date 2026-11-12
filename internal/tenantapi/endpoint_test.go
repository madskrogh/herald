package tenantapi_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/testing/protocmp"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/signing"
)

func TestCreateAndGetEndpoint(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			var app tenantv1.CreateApplicationResponse
			tr.mustInvoke(t, a, key, createApplication("Customer A"), &app)
			appID := app.GetApplication().GetId()

			var created tenantv1.CreateEndpointResponse
			tr.mustInvoke(t, a, key, createEndpoint(appID, "https://example.com/webhooks", []string{"invoice.*", "customer.>"}), &created)
			ep := created.GetEndpoint()
			if ep.GetId() == "" {
				t.Fatal("created Endpoint has no ID")
			}
			want := &tenantv1.Endpoint{
				Id:            ep.GetId(),
				ApplicationId: appID,
				Url:           "https://example.com/webhooks",
				Filter:        []string{"invoice.*", "customer.>"},
				CreatedAt:     ep.GetCreatedAt(),
			}
			if diff := cmp.Diff(want, ep, protocmp.Transform()); diff != "" {
				t.Errorf("CreateEndpoint (-want +got):\n%s", diff)
			}

			// "whsec_" and the base64 of 24 random bytes, which the Standard
			// Webhooks libraries accept.
			secret := created.GetSigningSecret()
			if !regexp.MustCompile(`^whsec_[A-Za-z0-9+/]{32}$`).MatchString(secret) {
				t.Errorf("signing secret %q is not whsec_ and 24 bytes of base64", secret)
			}
			if _, err := signing.Sign("msg_1", time.Now(), []byte(`{}`), []string{secret}); err != nil {
				t.Errorf("signing with the signing secret: %v", err)
			}

			var got tenantv1.GetEndpointResponse
			tr.mustInvoke(t, a, key, getEndpoint(appID, ep.GetId()), &got)
			if diff := cmp.Diff(want, got.GetEndpoint(), protocmp.Transform()); diff != "" {
				t.Errorf("GetEndpoint (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEndpointUnderAnotherApplicationIsNotFound(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var owner, other tenantv1.CreateApplicationResponse
	transports[0].mustInvoke(t, a, key, createApplication("Customer A"), &owner)
	transports[0].mustInvoke(t, a, key, createApplication("Customer B"), &other)
	var ep tenantv1.CreateEndpointResponse
	transports[0].mustInvoke(t, a, key, createEndpoint(owner.GetApplication().GetId(), "https://example.com/webhooks", nil), &ep)

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			if code := tr.invoke(t, a, "Bearer "+key, getEndpoint(other.GetApplication().GetId(), ep.GetEndpoint().GetId()), nil); code != codes.NotFound {
				t.Errorf("code = %s, want NotFound", code)
			}
			// Positive control: the same call under the Endpoint's own
			// Application succeeds.
			tr.mustInvoke(t, a, key, getEndpoint(owner.GetApplication().GetId(), ep.GetEndpoint().GetId()), nil)
		})
	}
}

func TestEndpointsGetSigningSecretsOfTheirOwn(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	var app tenantv1.CreateApplicationResponse
	transports[0].mustInvoke(t, a, key, createApplication("Customer A"), &app)

	var first, second tenantv1.CreateEndpointResponse
	transports[0].mustInvoke(t, a, key, createEndpoint(app.GetApplication().GetId(), "https://example.com/a", nil), &first)
	transports[0].mustInvoke(t, a, key, createEndpoint(app.GetApplication().GetId(), "https://example.com/b", nil), &second)
	if first.GetSigningSecret() == second.GetSigningSecret() {
		t.Errorf("two Endpoints both have signing secret %q", first.GetSigningSecret())
	}
}
