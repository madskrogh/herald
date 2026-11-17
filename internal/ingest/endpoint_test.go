package ingest_test

import (
	"errors"
	"testing"

	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/ingest/ingesttest"
)

// maxEndpoints is how many Endpoints an Application may have.
const maxEndpoints = 100

func TestApplicationHasAtMost100Endpoints(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	f := newFixture(t, store)
	for range maxEndpoints {
		f.endpoint(t, nil)
	}

	if _, err := f.createEndpoint(t, nil); !errors.Is(err, ingest.ErrTooManyEndpoints) {
		t.Errorf("CreateEndpoint %d: error %v, want ingest.ErrTooManyEndpoints", maxEndpoints+1, err)
	}

	// Positive control: the limit is per Application.
	application, err := store.CreateApplication(t.Context(), f.tenant.ID, "Customer B")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	other := fixture{store: store, tenant: f.tenant, application: application}
	if _, err := other.createEndpoint(t, nil); err != nil {
		t.Errorf("CreateEndpoint under another Application: %v", err)
	}
}

func TestConcurrentEndpointsStayWithinTheLimit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, pool := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	f := newFixture(t, store)
	for range maxEndpoints - 2 {
		f.endpoint(t, nil)
	}

	errs := make([]error, 8)
	concurrently(t, pool, len(errs), func(i int) {
		_, errs[i] = f.createEndpoint(t, nil)
	})

	var created int
	for i, err := range errs {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, ingest.ErrTooManyEndpoints):
			t.Errorf("CreateEndpoint %d: %v", i, err)
		}
	}
	if created != 2 {
		t.Errorf("%d concurrent CreateEndpoint calls succeeded, want 2: the limit is %d", created, maxEndpoints)
	}
}
