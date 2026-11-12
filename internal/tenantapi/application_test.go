package tenantapi_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
)

func TestCreateAndGetApplication(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			before := time.Now()

			var created tenantv1.CreateApplicationResponse
			tr.mustInvoke(t, a, key, createApplication("Customer A"), &created)
			app := created.GetApplication()
			if app.GetId() == "" {
				t.Fatal("created Application has no ID")
			}
			// Postgres's clock may be a little behind the test's.
			if at := app.GetCreatedAt().AsTime(); at.Before(before.Add(-time.Minute)) || at.After(time.Now().Add(time.Minute)) {
				t.Errorf("created_at = %v, want about %v", at, before)
			}
			want := &tenantv1.Application{Id: app.GetId(), Name: "Customer A", CreatedAt: app.GetCreatedAt()}
			if diff := cmp.Diff(want, app, protocmp.Transform()); diff != "" {
				t.Errorf("CreateApplication (-want +got):\n%s", diff)
			}

			var got tenantv1.GetApplicationResponse
			tr.mustInvoke(t, a, key, getApplication(app.GetId()), &got)
			if diff := cmp.Diff(want, got.GetApplication(), protocmp.Transform()); diff != "" {
				t.Errorf("GetApplication (-want +got):\n%s", diff)
			}
		})
	}
}
