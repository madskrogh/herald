package delivery_test

import (
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
)

func TestRecordASucceededAttempt(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, testBatch())
	lease := leaseOf(t, claim(t, in.store, now, 10), deliveryID1)
	attempt := delivery.Attempt{
		StartedAt:    now,
		StatusCode:   204,
		Latency:      1500 * time.Millisecond,
		ResponseBody: []byte("ok"),
	}

	if err := in.store.Record(t.Context(), lease, attempt, delivery.StateSucceeded, time.Time{}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := in.store.Delivery(t.Context(), lease.Delivery.ID)
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StateSucceeded {
		t.Errorf("state = %q, want %q", got.State, delivery.StateSucceeded)
	}
	want := []delivery.Attempt{{
		Number:       1,
		StartedAt:    now,
		StatusCode:   204,
		Latency:      1500 * time.Millisecond,
		ResponseBody: []byte("ok"),
	}}
	if diff := cmp.Diff(want, attempts(t, in.store, lease.Delivery.ID)); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
	// A Delivery that succeeded is never claimed again.
	if got := claim(t, in.store, now.Add(24*time.Hour), 10); slices.Contains(deliveryIDs(got), lease.Delivery.ID) {
		t.Errorf("Claim(now+24h) leased succeeded Delivery %s", lease.Delivery.ID)
	}
}

// TestRecordRefusesALeaseThatRanOut has worker A claim a Delivery and hang
// past its lease, so worker B claims it again. A's late success must not
// record over B, whose Attempt failed and scheduled the next.
func TestRecordRefusesALeaseThatRanOut(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, testBatch())
	leaseA := leaseOf(t, claim(t, in.store, now, 10), deliveryID1)
	leaseB := leaseOf(t, claim(t, in.store, now.Add(60*time.Second), 10), deliveryID1)
	attemptB := delivery.Attempt{StartedAt: now.Add(60 * time.Second), StatusCode: 500, Latency: 30 * time.Millisecond, ResponseBody: []byte("boom")}
	if err := in.store.Record(t.Context(), leaseB, attemptB, delivery.StatePending, now.Add(65*time.Second)); err != nil {
		t.Fatalf("Record under lease B: %v", err)
	}

	attemptA := delivery.Attempt{StartedAt: now, StatusCode: 200, Latency: 70 * time.Second}
	err := in.store.Record(t.Context(), leaseA, attemptA, delivery.StateSucceeded, time.Time{})

	if !errors.Is(err, delivery.ErrLeaseLost) {
		t.Errorf("Record under lease A: error = %v, want %v", err, delivery.ErrLeaseLost)
	}
	got, err := in.store.Delivery(t.Context(), leaseA.Delivery.ID)
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StatePending || !got.NextAttemptAt.Equal(now.Add(65*time.Second)) {
		t.Errorf("Delivery is %q, due at %s; want pending, due at now+65s as B left it", got.State, got.NextAttemptAt)
	}
	want := []delivery.Attempt{{Number: 1, StartedAt: now.Add(60 * time.Second), StatusCode: 500, Latency: 30 * time.Millisecond, ResponseBody: []byte("boom")}}
	if diff := cmp.Diff(want, attempts(t, in.store, leaseA.Delivery.ID)); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
}

// leaseOf returns the lease on the Delivery with the given ID, failing the
// test if there is none.
func leaseOf(t *testing.T, leases []delivery.Lease, deliveryID string) delivery.Lease {
	t.Helper()
	for _, l := range leases {
		if l.Delivery.ID.String() == deliveryID {
			return l
		}
	}
	t.Fatalf("no lease on Delivery %s among %v", deliveryID, deliveryIDs(leases))
	return delivery.Lease{}
}

// attempts calls store.Attempts and fails the test on an error.
func attempts(t *testing.T, store *delivery.Store, deliveryID uuid.UUID) []delivery.Attempt {
	t.Helper()
	got, err := store.Attempts(t.Context(), deliveryID)
	if err != nil {
		t.Fatalf("Attempts(%s): %v", deliveryID, err)
	}
	return got
}
