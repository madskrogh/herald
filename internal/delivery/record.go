package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/madskrogh/herald/internal/delivery/deliverydb"
)

// ErrLeaseLost is returned when recording an Attempt under a lease that is no
// longer the Delivery's current one: it ran out and another worker claimed
// the Delivery.
var ErrLeaseLost = errors.New("lease lost")

// Attempt is one HTTP request made for a Delivery.
type Attempt struct {
	// Number counts the Delivery's Attempts from 1. Record sets it.
	Number    int
	StartedAt time.Time
	// StatusCode is the response's status, or 0 if there was no response.
	StatusCode int
	// Latency is how long the request took, up to the response headers or
	// the error. It is stored in whole milliseconds.
	Latency time.Duration
	// ResponseBody is the start of the response body.
	ResponseBody []byte
	// Error says why there was no response (a timeout or connection error),
	// or is empty.
	Error string
}

// Outcome is where an Attempt leaves its Delivery.
type Outcome struct {
	State State
	// NextAttemptAt is when the next Attempt is due. Only a pending Delivery
	// has one.
	NextAttemptAt time.Time
}

// Succeeded is the Outcome of a successful Attempt.
func Succeeded() Outcome { return Outcome{State: StateSucceeded} }

// Failed is the Outcome of the last Attempt the retry schedule allows.
func Failed() Outcome { return Outcome{State: StateFailed} }

// RetryAt is the Outcome of a failed Attempt with another due at t.
func RetryAt(t time.Time) Outcome { return Outcome{State: StatePending, NextAttemptAt: t} }

// Record records an Attempt made under lease and moves the Delivery on to
// the Outcome. If lease is no longer the Delivery's current lease, Record
// records nothing and returns an error wrapping ErrLeaseLost.
func (s *Store) Record(ctx context.Context, lease Lease, a Attempt, o Outcome) error {
	next := pgtype.Timestamptz{}
	if o.State == StatePending {
		next = pgtype.Timestamptz{Time: o.NextAttemptAt, Valid: true}
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		number, err := q.FinishAttempt(ctx, deliverydb.FinishAttemptParams{
			State:         string(o.State),
			NextAttemptAt: next,
			ID:            lease.Delivery.ID,
			LeaseID:       lease.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		if err != nil {
			return fmt.Errorf("end lease: %w", err)
		}
		err = q.InsertAttempt(ctx, deliverydb.InsertAttemptParams{
			DeliveryID: lease.Delivery.ID,
			Number:     number,
			StartedAt:  a.StartedAt,
			StatusCode: pgtype.Int4{Int32: int32(a.StatusCode), Valid: a.StatusCode != 0},
			LatencyMs:  int32(a.Latency.Milliseconds()),
			// pgx sends a nil slice as NULL; an Attempt without a response
			// has an empty body.
			ResponseBody: append([]byte{}, a.ResponseBody...),
			Error:        pgtype.Text{String: a.Error, Valid: a.Error != ""},
		})
		if err != nil {
			return fmt.Errorf("insert attempt %d: %w", number, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record attempt for delivery %s: %w", lease.Delivery.ID, err)
	}
	return nil
}

// Attempts returns the Delivery's Attempts, oldest first.
func (s *Store) Attempts(ctx context.Context, deliveryID uuid.UUID) ([]Attempt, error) {
	rows, err := s.q.ListAttempts(ctx, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("list attempts for delivery %s: %w", deliveryID, err)
	}
	attempts := make([]Attempt, 0, len(rows))
	for _, r := range rows {
		attempts = append(attempts, Attempt{
			Number:       int(r.Number),
			StartedAt:    r.StartedAt,
			StatusCode:   int(r.StatusCode.Int32),
			Latency:      time.Duration(r.LatencyMs) * time.Millisecond,
			ResponseBody: r.ResponseBody,
			Error:        r.Error.String,
		})
	}
	return attempts, nil
}
