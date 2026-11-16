package delivery_test

import (
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
)

// TestClaimLeasesDeliveriesFor60Seconds claims the Deliveries in testBatch and
// never records an Attempt, as when the worker that claimed them dies. They
// are due again once the lease runs out, under a new lease.
func TestClaimLeasesDeliveriesFor60Seconds(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, testBatch())
	all := []uuid.UUID{uuid.MustParse(deliveryID1), uuid.MustParse(deliveryID2), uuid.MustParse(deliveryID3)}

	first := claim(t, in.store, now, 10)
	if diff := cmp.Diff(all, deliveryIDs(first)); diff != "" {
		t.Fatalf("Claim(now) (-want +got):\n%s", diff)
	}
	if got := claim(t, in.store, now.Add(59*time.Second), 10); len(got) != 0 {
		t.Errorf("Claim(now+59s) = %v, want none: their leases still run", deliveryIDs(got))
	}
	again := claim(t, in.store, now.Add(60*time.Second), 10)
	if diff := cmp.Diff(all, deliveryIDs(again)); diff != "" {
		t.Fatalf("Claim(now+60s) (-want +got):\n%s", diff)
	}
	for i := range again {
		if again[i].ID == first[i].ID {
			t.Errorf("Delivery %s has lease %s again, want a new one", again[i].Delivery.ID, again[i].ID)
		}
	}
}

// claim calls store.Claim and fails the test on an error.
func claim(t *testing.T, store *delivery.Store, now time.Time, limit int) []delivery.Lease {
	t.Helper()
	leases, err := store.Claim(t.Context(), now, limit)
	if err != nil {
		t.Fatalf("Claim(%s, %d): %v", now, limit, err)
	}
	return leases
}

// deliveryIDs returns the IDs of the leased Deliveries, sorted.
func deliveryIDs(leases []delivery.Lease) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(leases))
	for _, l := range leases {
		ids = append(ids, l.Delivery.ID)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return a.Compare(b) })
	return ids
}
