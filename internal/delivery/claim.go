package delivery

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/madskrogh/herald/internal/delivery/deliverydb"
)

// leaseDuration is how long a worker holds a Delivery it claimed. It is well
// above the 15 seconds an Attempt may take, so a live worker records its
// Attempt in time, and it bounds how long a Delivery whose worker died waits
// before it is due again.
const leaseDuration = 60 * time.Second

// maxInFlight is the most Attempts in flight to one Endpoint, across all
// dispatcher replicas (docs/design.md, Isolation).
const maxInFlight = 4

// Lease is a worker's claim on a Delivery, for one Attempt.
type Lease struct {
	// ID identifies this claim. Only the holder of the Delivery's current
	// lease can record its Attempt.
	ID       uuid.UUID
	Delivery Delivery
}

// Claim leases up to limit Deliveries that are due at now, for one Attempt
// each, keeping every Endpoint to maxInFlight unexpired leases across all
// dispatcher replicas. A Delivery whose lease runs out without an Attempt
// recorded is due again, so a worker that dies doesn't strand it.
//
// An Endpoint that another replica is claiming for at the same moment is
// skipped; a later round picks it up.
func (s *Store) Claim(ctx context.Context, now time.Time, limit int) ([]Lease, error) {
	var leases []Lease
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		endpoints, err := q.ListDueEndpoints(ctx, deliverydb.ListDueEndpointsParams{
			Now:          now,
			MaxEndpoints: int32(limit),
			MaxInFlight:  maxInFlight,
		})
		if err != nil {
			return fmt.Errorf("list due endpoints: %w", err)
		}
		for _, endpointID := range endpoints {
			if len(leases) >= limit {
				break
			}
			claimed, err := claimEndpoint(ctx, q, endpointID, now, min(maxInFlight, limit-len(leases)))
			if err != nil {
				return err
			}
			leases = append(leases, claimed...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	return leases, nil
}

// claimEndpoint leases up to limit of the Endpoint's due Deliveries, as many
// as its cap leaves room for. It takes the Endpoint's lock for the rest of
// the transaction, so the count and the leases it adds can't interleave with
// another replica's; if another transaction holds the lock, it leases none.
func claimEndpoint(ctx context.Context, q *deliverydb.Queries, endpointID uuid.UUID, now time.Time, limit int) ([]Lease, error) {
	locked, err := q.TryLockEndpoint(ctx, endpointID)
	if err != nil {
		return nil, fmt.Errorf("lock endpoint %s: %w", endpointID, err)
	}
	if !locked {
		return nil, nil
	}
	inFlight, err := q.CountInFlight(ctx, deliverydb.CountInFlightParams{EndpointID: endpointID, Now: now})
	if err != nil {
		return nil, fmt.Errorf("count attempts in flight to endpoint %s: %w", endpointID, err)
	}
	room := min(limit, maxInFlight-int(inFlight))
	if room <= 0 {
		return nil, nil
	}
	rows, err := q.ClaimEndpointDeliveries(ctx, deliverydb.ClaimEndpointDeliveriesParams{
		LeasedUntil:   now.Add(leaseDuration),
		EndpointID:    endpointID,
		Now:           now,
		MaxDeliveries: int32(room),
	})
	if err != nil {
		return nil, fmt.Errorf("lease deliveries to endpoint %s: %w", endpointID, err)
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
