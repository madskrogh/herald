// Package delivery owns herald-dispatcher's delivery schema: its migrations,
// the Deliveries stored in it, and the intake that stores the Batches the
// relay in herald-api hands off (ADR 0002).
package delivery

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/delivery/deliverydb"
	"github.com/madskrogh/herald/internal/migrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies the delivery schema's migrations that haven't been applied
// to the database at databaseURL. It is run as a one-shot step before
// herald-dispatcher starts, never by the service itself.
func Migrate(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	return migrate.Run(ctx, databaseURL, "delivery", fsys, logger)
}

// ErrNotFound is returned when a Delivery doesn't exist.
var ErrNotFound = errors.New("not found")

// State is where a Delivery is in its life: pending until an Attempt
// succeeds or the retry schedule runs out.
type State string

// The states of a Delivery.
const (
	StatePending   State = "pending"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
)

// Delivery is one Message on its way to one Endpoint.
type Delivery struct {
	ID        uuid.UUID
	MessageID uuid.UUID
	TenantID  uuid.UUID
	EventType string
	// The JSON payload exactly as the Tenant submitted it.
	Payload  []byte
	Endpoint Endpoint
	State    State
	// When the next Attempt is due.
	NextAttemptAt time.Time
}

// Endpoint is a Delivery's copy of its Endpoint's details, taken when the
// Message was accepted.
type Endpoint struct {
	ID  uuid.UUID
	URL string
	// The signing secrets encrypted with the keyring (internal/keyring), with
	// the Endpoint's ID in canonical text form as associated data.
	SigningSecrets [][]byte
}

// Store reads and writes the delivery schema.
type Store struct {
	pool *pgxpool.Pool
	q    *deliverydb.Queries
}

// NewStore returns a Store that uses pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: deliverydb.New(pool)}
}

// insert stores messages and deliveries in one transaction, leaving any that
// are already stored as they are.
func (s *Store) insert(ctx context.Context, messages []deliverydb.InsertMessageParams, deliveries []deliverydb.InsertDeliveryParams) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		q.InsertMessage(ctx, messages).Exec(func(i int, e error) {
			if e != nil && err == nil {
				err = fmt.Errorf("insert message %s: %w", messages[i].ID, e)
			}
		})
		if err != nil {
			return err
		}
		q.InsertDelivery(ctx, deliveries).Exec(func(i int, e error) {
			if e != nil && err == nil {
				err = fmt.Errorf("insert delivery %s: %w", deliveries[i].ID, e)
			}
		})
		return err
	})
}

// Delivery returns the Delivery with the given ID.
func (s *Store) Delivery(ctx context.Context, id uuid.UUID) (Delivery, error) {
	row, err := s.q.GetDelivery(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, fmt.Errorf("get delivery %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("get delivery %s: %w", id, err)
	}
	return Delivery{
		ID:        row.ID,
		MessageID: row.MessageID,
		TenantID:  row.TenantID,
		EventType: row.EventType,
		Payload:   row.Payload,
		Endpoint: Endpoint{
			ID:             row.EndpointID,
			URL:            row.EndpointURL,
			SigningSecrets: row.SigningSecrets,
		},
		State:         State(row.State),
		NextAttemptAt: row.NextAttemptAt,
	}, nil
}
