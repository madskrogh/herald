package ingest

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/madskrogh/herald/internal/ingest/ingestdb"
)

// OutboxMessage is a Message waiting in the outbox, with everything the
// dispatchers need to make its Deliveries' Attempts.
type OutboxMessage struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	EventType string
	// The JSON payload exactly as the Tenant submitted it.
	Payload    []byte
	Deliveries []OutboxDelivery
}

// OutboxDelivery is a Delivery waiting in the outbox, with a copy of its
// Endpoint's details.
type OutboxDelivery struct {
	ID         uuid.UUID
	EndpointID uuid.UUID
	URL        string
	// The Endpoint's signing secrets, encrypted with the keyring, oldest
	// first.
	SigningSecrets [][]byte
}

// Handoff is what one HandOff did.
type Handoff struct {
	// Claimed is how many Messages it claimed from the outbox.
	Claimed int
	// HandedOff is how many of them it handed off.
	HandedOff int
}

// HandOff claims up to limit of the Messages waiting in the outbox, oldest
// first, and passes them to send. send returns how many of them, from the
// start, it handed off; HandOff marks those handed off at now.
//
// The claim and the mark are one transaction, which stays open while send
// runs. The claimed Messages stay locked until it ends, so a concurrent
// HandOff skips them and claims others. If send returns an error, nothing is
// marked, and the Messages wait in the outbox for a later round.
func (s *Store) HandOff(ctx context.Context, limit int, now time.Time, send func(context.Context, []OutboxMessage) (int, error)) (Handoff, error) {
	var h Handoff
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// limit is a small configured count, well within int32.
		messages, err := claimOutbox(ctx, q, int32(limit))
		if err != nil || len(messages) == 0 {
			return err
		}
		h.Claimed = len(messages)
		n, err := send(ctx, messages)
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, n)
		for i, m := range messages[:n] {
			ids[i] = m.ID
		}
		if err := q.MarkHandedOff(ctx, ingestdb.MarkHandedOffParams{Now: now, Ids: ids}); err != nil {
			return fmt.Errorf("mark handed off: %w", err)
		}
		h.HandedOff = n
		return nil
	})
	if err != nil {
		return Handoff{}, fmt.Errorf("hand off outbox: %w", err)
	}
	return h, nil
}

// claimOutbox claims up to limit outbox Messages, with their Deliveries.
func claimOutbox(ctx context.Context, q *ingestdb.Queries, limit int32) ([]OutboxMessage, error) {
	rows, err := q.ClaimOutbox(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbox: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	messages := make([]OutboxMessage, len(rows))
	byID := make(map[uuid.UUID]*OutboxMessage, len(rows))
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		messages[i] = OutboxMessage{
			ID:        row.ID,
			TenantID:  row.TenantID,
			EventType: row.EventType,
			Payload:   row.Payload,
		}
		byID[row.ID] = &messages[i]
		ids[i] = row.ID
	}
	deliveries, err := q.ListOutboxDeliveries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list outbox deliveries: %w", err)
	}
	for _, d := range deliveries {
		m := byID[d.MessageID]
		m.Deliveries = append(m.Deliveries, OutboxDelivery{
			ID:             d.ID,
			EndpointID:     d.EndpointID,
			URL:            d.URL,
			SigningSecrets: d.SigningSecrets,
		})
	}
	return messages, nil
}
