package delivery_test

import (
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
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

// TestClaimLeasesAtMost4PerEndpoint claims Deliveries to an Endpoint with 10
// due and to one with 1. Leases that run out stop counting toward the cap.
func TestClaimLeasesAtMost4PerEndpoint(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, batchToEndpoints(map[string]int{endpointID1: 10, endpointID2: 1}))

	if diff := cmp.Diff(perEndpoint{endpointID1: 4, endpointID2: 1}, countPerEndpoint(claim(t, in.store, now, 20))); diff != "" {
		t.Errorf("Claim(now) (-want +got):\n%s", diff)
	}
	// Endpoint 1 still has 4 Attempts in flight.
	if diff := cmp.Diff(perEndpoint{}, countPerEndpoint(claim(t, in.store, now.Add(30*time.Second), 20))); diff != "" {
		t.Errorf("Claim(now+30s) (-want +got):\n%s", diff)
	}
	// The leases have run out, so they no longer count as in flight.
	if diff := cmp.Diff(perEndpoint{endpointID1: 4, endpointID2: 1}, countPerEndpoint(claim(t, in.store, now.Add(60*time.Second), 20))); diff != "" {
		t.Errorf("Claim(now+60s) (-want +got):\n%s", diff)
	}
}

// TestClaimSkipsEndpointsAtTheCap checks that an Endpoint with 4 Attempts in
// flight doesn't use up a claim's limit, so a slow Endpoint doesn't hold up
// Deliveries to others. Endpoint 1 sorts first among Deliveries due at the
// same time, so a claim that took it into account would return nothing.
func TestClaimSkipsEndpointsAtTheCap(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, batchToEndpoints(map[string]int{endpointID1: 10}))
	if diff := cmp.Diff(perEndpoint{endpointID1: 4}, countPerEndpoint(claim(t, in.store, now, 4))); diff != "" {
		t.Fatalf("Claim(now, 4) (-want +got):\n%s", diff)
	}
	in.send(t, batchToEndpoints(map[string]int{endpointID2: 1}))

	if diff := cmp.Diff(perEndpoint{endpointID2: 1}, countPerEndpoint(claim(t, in.store, now, 1))); diff != "" {
		t.Errorf("Claim(now, 1) (-want +got):\n%s", diff)
	}
}

// TestClaimHoldsTheCapAcrossReplicas claims from many goroutines at once, each
// standing in for a dispatcher replica: together they lease no more than 4
// Deliveries to one Endpoint.
func TestClaimHoldsTheCapAcrossReplicas(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, batchToEndpoints(map[string]int{endpointID1: 10, endpointID2: 1}))

	const replicas = 8
	results := make([][]delivery.Lease, replicas)
	errs := make([]error, replicas)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range replicas {
		wg.Go(func() {
			<-start
			results[i], errs[i] = in.store.Claim(t.Context(), now, 20)
		})
	}
	close(start)
	wg.Wait()

	var all []delivery.Lease
	for i := range replicas {
		if errs[i] != nil {
			t.Fatalf("replica %d: Claim: %v", i, errs[i])
		}
		all = append(all, results[i]...)
	}
	if diff := cmp.Diff(perEndpoint{endpointID1: 4, endpointID2: 1}, countPerEndpoint(all)); diff != "" {
		t.Errorf("leased together (-want +got):\n%s", diff)
	}
}

// batchToEndpoints returns a Batch of one Message with the given number of
// Deliveries to each Endpoint.
func batchToEndpoints(counts map[string]int) *handoffv1.Batch {
	m := &handoffv1.Message{
		Id:        uuid.NewV7().String(),
		TenantId:  tenantID,
		EventType: "invoice.paid",
		Payload:   []byte(`{"invoice": 42}`),
	}
	for endpointID, n := range counts {
		endpoint := &handoffv1.Endpoint{
			Id:             endpointID,
			Url:            "https://example.com/" + endpointID,
			SigningSecrets: []*handoffv1.SigningSecret{{Ciphertext: []byte("secret")}},
		}
		for range n {
			m.Deliveries = append(m.Deliveries, &handoffv1.Delivery{Id: uuid.NewV7().String(), Endpoint: endpoint})
		}
	}
	return &handoffv1.Batch{Messages: []*handoffv1.Message{m}}
}

// perEndpoint counts leases by Endpoint ID.
type perEndpoint map[string]int

func countPerEndpoint(leases []delivery.Lease) perEndpoint {
	counts := perEndpoint{}
	for _, l := range leases {
		counts[l.Delivery.Endpoint.ID.String()]++
	}
	return counts
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
