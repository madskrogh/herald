package delivery

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/delivery/deliverydb"
)

// leaseDuration is how long a worker holds a Delivery it claimed. It is well
// above the 15 seconds an Attempt may take, so a live worker records its
// Attempt in time, and it bounds how long a Delivery whose worker died waits
// before it is due again.
const leaseDuration = 60 * time.Second

// Lease is a worker's claim on a Delivery, for one Attempt.
type Lease struct {
	// ID identifies this claim. Only the holder of the Delivery's current
	// lease can record its Attempt.
	ID       uuid.UUID
	Delivery Delivery
}

// Claim leases up to limit Deliveries that are due at now, for one Attempt
// each. A Delivery whose lease runs out without an Attempt recorded is due
// again, so a worker that dies doesn't strand it.
func (s *Store) Claim(ctx context.Context, now time.Time, limit int) ([]Lease, error) {
	rows, err := s.q.ClaimDueDeliveries(ctx, deliverydb.ClaimDueDeliveriesParams{
		LeasedUntil:   now.Add(leaseDuration),
		Now:           now,
		MaxDeliveries: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	leases := make([]Lease, 0, len(rows))
	for _, r := range rows {
		leases = append(leases, Lease{
			ID: r.LeaseID,
			Delivery: Delivery{
				ID:        r.ID,
				MessageID: r.MessageID,
				TenantID:  r.TenantID,
				EventType: r.EventType,
				Payload:   r.Payload,
				Endpoint: Endpoint{
					ID:             r.EndpointID,
					URL:            r.EndpointURL,
					SigningSecrets: r.SigningSecrets,
				},
				State:         State(r.State),
				NextAttemptAt: r.NextAttemptAt,
			},
		})
	}
	return leases, nil
}
