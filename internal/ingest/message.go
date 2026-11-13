package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/madskrogh/herald/internal/eventtype"
	"github.com/madskrogh/herald/internal/ingest/ingestdb"
)

// idempotencyKeyTTL is how long an Idempotency-Key holds the Message it was
// first sent with. A repeat within it returns that Message; after it, the key
// creates a new one.
const idempotencyKeyTTL = 24 * time.Hour

// NewMessage is a Message a Tenant submits for one of its Applications.
type NewMessage struct {
	TenantID      uuid.UUID
	ApplicationID uuid.UUID
	EventType     eventtype.EventType
	// Payload is the JSON payload exactly as the Tenant submitted it.
	Payload []byte
	// IdempotencyKey is the Tenant's Idempotency-Key for the Message, or
	// empty for none.
	IdempotencyKey string
}

// Message is a single notification a Tenant submitted for one Application,
// with the Deliveries Herald worked out for it.
type Message struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	ApplicationID uuid.UUID
	EventType     string
	Payload       []byte
	CreatedAt     time.Time
	// HandedOffAt is when the Message's Deliveries were handed off to the
	// dispatchers, or zero while they wait in the outbox.
	HandedOffAt time.Time
	Deliveries  []Delivery
}

// Delivery is one Message on its way to one Endpoint.
type Delivery struct {
	ID         uuid.UUID
	EndpointID uuid.UUID
}

// CreateMessage accepts a Message at the time now, and works out its
// Deliveries: one to each Endpoint of the Application whose filter matches the
// Event type. The Message and its Deliveries are written in one transaction,
// waiting in the outbox for the relay.
//
// If one of the Tenant's Messages created less than 24 hours before now holds
// m's Idempotency-Key, CreateMessage writes nothing and returns that Message
// instead. It reports whether it created the Message.
func (s *Store) CreateMessage(ctx context.Context, m NewMessage, now time.Time) (Message, bool, error) {
	var (
		msg     Message
		created bool
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		endpointIDs, err := matchingEndpoints(ctx, q, m)
		if err != nil {
			return err
		}
		if m.IdempotencyKey != "" {
			// Keys expire lazily: the Message that holds an expired key keeps
			// it until the key is reused, and gives it up here.
			err := q.ReleaseIdempotencyKey(ctx, ingestdb.ReleaseIdempotencyKeyParams{
				TenantID:       m.TenantID,
				IdempotencyKey: m.IdempotencyKey,
				ExpiredBy:      now.Add(-idempotencyKeyTTL),
			})
			if err != nil {
				return fmt.Errorf("release expired idempotency key: %w", err)
			}
		}
		// Concurrent calls with the same key wait here on the unique index
		// until the first commits; then they insert nothing.
		row, err := q.CreateMessage(ctx, ingestdb.CreateMessageParams{
			TenantID:       m.TenantID,
			ApplicationID:  m.ApplicationID,
			EventType:      m.EventType.String(),
			Payload:        m.Payload,
			IdempotencyKey: m.IdempotencyKey,
			Now:            now,
			HasDeliveries:  len(endpointIDs) > 0,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			msg, err = messageByIdempotencyKey(ctx, q, m)
			return err
		}
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
		msg, created = toMessage(row), true
		deliveries, err := q.CreateDeliveries(ctx, ingestdb.CreateDeliveriesParams{
			MessageID:   row.ID,
			EndpointIds: endpointIDs,
		})
		if err != nil {
			return fmt.Errorf("insert deliveries: %w", err)
		}
		for _, d := range deliveries {
			msg.Deliveries = append(msg.Deliveries, Delivery(d))
		}
		return nil
	})
	if err != nil {
		return Message{}, false, fmt.Errorf("create message: %w", err)
	}
	return msg, created, nil
}

// messageByIdempotencyKey returns the Message, with its Deliveries, that stopped
// m from being inserted: the Tenant's Message that holds m's Idempotency-Key.
// If there is none, m's Application doesn't belong to the Tenant.
func messageByIdempotencyKey(ctx context.Context, q *ingestdb.Queries, m NewMessage) (Message, error) {
	notFound := fmt.Errorf("application %s: %w", m.ApplicationID, ErrNotFound)
	if m.IdempotencyKey == "" {
		return Message{}, notFound
	}
	row, err := q.GetMessageByIdempotencyKey(ctx, ingestdb.GetMessageByIdempotencyKeyParams{
		TenantID:       m.TenantID,
		IdempotencyKey: m.IdempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, notFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("get message by idempotency key: %w", err)
	}
	msg := toMessage(row)
	deliveries, err := q.ListDeliveries(ctx, row.ID)
	if err != nil {
		return Message{}, fmt.Errorf("list deliveries of message %s: %w", row.ID, err)
	}
	for _, d := range deliveries {
		msg.Deliveries = append(msg.Deliveries, Delivery(d))
	}
	return msg, nil
}

// matchingEndpoints returns the IDs of the Endpoints under m's Application
// whose filters match m's Event type, in ID order.
func matchingEndpoints(ctx context.Context, q *ingestdb.Queries, m NewMessage) ([]uuid.UUID, error) {
	rows, err := q.ListEndpointFilters(ctx, ingestdb.ListEndpointFiltersParams{
		TenantID:      m.TenantID,
		ApplicationID: m.ApplicationID,
	})
	if err != nil {
		return nil, fmt.Errorf("list endpoint filters: %w", err)
	}
	var ids []uuid.UUID
	for _, row := range rows {
		filter, err := eventtype.ParseFilter(row.Filter)
		if err != nil {
			// Filters are validated when an Endpoint is created.
			return nil, fmt.Errorf("endpoint %s: %w", row.ID, err)
		}
		if filter.Matches(m.EventType) {
			ids = append(ids, row.ID)
		}
	}
	return ids, nil
}

func toMessage(row ingestdb.IngestMessage) Message {
	return Message{
		ID:            row.ID,
		TenantID:      row.TenantID,
		ApplicationID: row.ApplicationID,
		EventType:     row.EventType,
		Payload:       row.Payload,
		CreatedAt:     row.CreatedAt,
		HandedOffAt:   row.HandedOffAt.Time, // Zero when NULL.
	}
}
