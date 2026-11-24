package e2e

import (
	"context"
	"fmt"
	"slices"
	"uuid"
)

// The harness reads Herald's Postgres only through the columns in the column
// list (spec/postgres-columns.md), with its own queries (postgres/), never
// through Herald's stores: so it holds any build to the list, not to the
// code's own reading of its tables.

// DeliveryState is a Delivery's state, as the column list gives
// delivery.deliveries.state.
type DeliveryState string

// The states of a Delivery (R-13).
const (
	StatePending   DeliveryState = "pending"
	StateSucceeded DeliveryState = "succeeded"
	StateFailed    DeliveryState = "failed"
)

// Delivery is what the harness reads of a Delivery.
type Delivery struct {
	ID         uuid.UUID
	MessageID  uuid.UUID
	EndpointID uuid.UUID
	State      DeliveryState
}

// MessageStatus is where a Message is: handed off, Parked, or still in the
// Outbox.
type MessageStatus struct {
	ID        uuid.UUID
	HandedOff bool
	Parked    bool
}

// MessageDeliveries returns the Deliveries of the Message with the given ID,
// whatever their state, ordered by ID.
func (s *Stack) MessageDeliveries(ctx context.Context, messageID uuid.UUID) ([]Delivery, error) {
	rows, err := s.postgres.ListMessageDeliveries(ctx, messageID)
	if err != nil {
		return nil, fmt.Errorf("read the deliveries of message %s: %w", messageID, err)
	}
	deliveries := make([]Delivery, len(rows))
	for i, r := range rows {
		deliveries[i] = Delivery{ID: r.ID, MessageID: r.MessageID, EndpointID: r.EndpointID, State: DeliveryState(r.State)}
	}
	return deliveries, nil
}

// RecordedAttempts returns how many Attempts herald-dispatcher recorded for
// the Delivery.
func (s *Stack) RecordedAttempts(ctx context.Context, deliveryID uuid.UUID) (int, error) {
	n, err := s.postgres.CountAttempts(ctx, deliveryID)
	if err != nil {
		return 0, fmt.Errorf("count the attempts of delivery %s: %w", deliveryID, err)
	}
	return int(n), nil
}

// TenantMessages returns where each of the Tenant's Messages is, ordered by
// ID.
func (s *Stack) TenantMessages(ctx context.Context, tenantID uuid.UUID) ([]MessageStatus, error) {
	rows, err := s.postgres.ListTenantMessages(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("read the messages of tenant %s: %w", tenantID, err)
	}
	messages := make([]MessageStatus, len(rows))
	for i, r := range rows {
		messages[i] = MessageStatus(r)
	}
	return messages, nil
}

func anyPending(deliveries []Delivery) bool {
	return slices.ContainsFunc(deliveries, func(d Delivery) bool { return d.State == StatePending })
}
