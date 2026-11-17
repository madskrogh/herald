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
			application := created.GetApplication()
			if application.GetId() == "" {
				t.Fatal("created Application has no ID")
			}
			// Postgres's clock may be a little behind the test's.
			if at := application.GetCreatedAt().AsTime(); at.Before(before.Add(-time.Minute)) || at.After(time.Now().Add(time.Minute)) {
				t.Errorf("created_at = %v, want about %v", at, before)
			}
			want := &tenantv1.Application{Id: application.GetId(), Name: "Customer A", CreatedAt: application.GetCreatedAt()}
			if diff := cmp.Diff(want, application, protocmp.Transform()); diff != "" {
				t.Errorf("CreateApplication (-want +got):\n%s", diff)
			}

			var got tenantv1.GetApplicationResponse
			tr.mustInvoke(t, a, key, getApplication(application.GetId()), &got)
			if diff := cmp.Diff(want, got.GetApplication(), protocmp.Transform()); diff != "" {
				t.Errorf("GetApplication (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListApplications(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newAPI(t)
	key := a.newTenant(t, "Acme")
	other := a.newTenant(t, "Globex")
	for _, name := range []string{"Customer A", "Customer B", "Customer C"} {
		setup.mustInvoke(t, a, key, createApplication(name), nil)
	}
	setup.mustInvoke(t, a, other, createApplication("Not Acme's"), nil)

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()

			t.Run("in pages, in the order they were created", func(t *testing.T) {
				t.Parallel()
				var first tenantv1.ListApplicationsResponse
				tr.mustInvoke(t, a, key, listApplications(2, ""), &first)
				if first.GetNextPageToken() == "" {
					t.Fatal("first page has no next_page_token")
				}
				var second tenantv1.ListApplicationsResponse
				tr.mustInvoke(t, a, key, listApplications(2, first.GetNextPageToken()), &second)
				if second.GetNextPageToken() != "" {
					t.Errorf("last page has next_page_token %q, want none", second.GetNextPageToken())
				}
				got := [][]string{applicationNames(&first), applicationNames(&second)}
				want := [][]string{{"Customer A", "Customer B"}, {"Customer C"}}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("pages of Application names (-want +got):\n%s", diff)
				}
			})

			t.Run("on one page by default", func(t *testing.T) {
				t.Parallel()
				var page tenantv1.ListApplicationsResponse
				tr.mustInvoke(t, a, key, listApplications(0, ""), &page)
				want := []string{"Customer A", "Customer B", "Customer C"}
				if diff := cmp.Diff(want, applicationNames(&page)); diff != "" {
					t.Errorf("Application names (-want +got):\n%s", diff)
				}
				if page.GetNextPageToken() != "" {
					t.Errorf("next_page_token = %q, want none", page.GetNextPageToken())
				}
			})
		})
	}
}

func applicationNames(page *tenantv1.ListApplicationsResponse) []string {
	var names []string
	for _, application := range page.GetApplications() {
		names = append(names, application.GetName())
	}
	return names
}
