package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/madskrogh/herald/internal/ingest/ingestdb"
)

// Application is one of a Tenant's customers: the owner of a set of
// Endpoints.
type Application struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	CreatedAt time.Time
}

// CreateApplication creates an Application for the Tenant.
func (s *Store) CreateApplication(ctx context.Context, tenantID uuid.UUID, name string) (Application, error) {
	row, err := s.q.CreateApplication(ctx, ingestdb.CreateApplicationParams{TenantID: tenantID, Name: name})
	if err != nil {
		return Application{}, fmt.Errorf("create application: %w", err)
	}
	return Application(row), nil
}

// Application returns the Tenant's Application with the given ID. Another
// Tenant's Application is reported as ErrNotFound.
func (s *Store) Application(ctx context.Context, tenantID, id uuid.UUID) (Application, error) {
	row, err := s.q.GetApplication(ctx, ingestdb.GetApplicationParams{TenantID: tenantID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Application{}, fmt.Errorf("get application %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Application{}, fmt.Errorf("get application %s: %w", id, err)
	}
	return Application(row), nil
}
