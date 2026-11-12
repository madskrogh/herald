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

// Endpoint is a URL registered under an Application, to which Herald delivers
// webhooks.
type Endpoint struct {
	ID            uuid.UUID
	ApplicationID uuid.UUID
	URL           string
	// Filter holds Event type patterns (internal/eventtype). It is never nil;
	// empty accepts every Event type.
	Filter    []string
	CreatedAt time.Time
}

// NewEndpoint is an Endpoint to create, with its first signing secret.
type NewEndpoint struct {
	// ID is chosen by the caller, since the signing secret is encrypted with
	// it as associated data.
	ID            uuid.UUID
	TenantID      uuid.UUID
	ApplicationID uuid.UUID
	URL           string
	Filter        []string
	// SigningSecretCiphertext is the signing secret, encrypted with the
	// keyring. The Store never sees the secret itself.
	SigningSecretCiphertext []byte
}

// CreateEndpoint creates an Endpoint and its signing secret. If the
// Application doesn't exist or belongs to another Tenant, it returns
// ErrNotFound.
func (s *Store) CreateEndpoint(ctx context.Context, e NewEndpoint) (Endpoint, error) {
	filter := e.Filter
	if filter == nil {
		filter = []string{} // nil would be stored as NULL.
	}
	var ep Endpoint
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.CreateEndpoint(ctx, ingestdb.CreateEndpointParams{
			ID:            e.ID,
			URL:           e.URL,
			Filter:        filter,
			TenantID:      e.TenantID,
			ApplicationID: e.ApplicationID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("application %s: %w", e.ApplicationID, ErrNotFound)
		}
		if err != nil {
			return err
		}
		ep = Endpoint(row)
		return q.CreateSigningSecret(ctx, ingestdb.CreateSigningSecretParams{
			EndpointID: e.ID,
			Ciphertext: e.SigningSecretCiphertext,
		})
	})
	if err != nil {
		return Endpoint{}, fmt.Errorf("create endpoint: %w", err)
	}
	return ep, nil
}

// Endpoint returns the Endpoint with the given ID under the Tenant's
// Application. An Endpoint under another Application, or another Tenant's,
// is reported as ErrNotFound.
func (s *Store) Endpoint(ctx context.Context, tenantID, applicationID, id uuid.UUID) (Endpoint, error) {
	row, err := s.q.GetEndpoint(ctx, ingestdb.GetEndpointParams{
		TenantID:      tenantID,
		ApplicationID: applicationID,
		ID:            id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Endpoint{}, fmt.Errorf("get endpoint %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Endpoint{}, fmt.Errorf("get endpoint %s: %w", id, err)
	}
	return Endpoint(row), nil
}

// ListEndpoints returns up to maxResults of the Endpoints under the Tenant's
// Application whose IDs come after the ID after, in ID order. The nil UUID
// comes before every ID. If the Application doesn't exist or belongs to
// another Tenant, it returns ErrNotFound rather than an empty list.
func (s *Store) ListEndpoints(ctx context.Context, tenantID, applicationID, after uuid.UUID, maxResults int32) ([]Endpoint, error) {
	if _, err := s.Application(ctx, tenantID, applicationID); err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	rows, err := s.q.ListEndpoints(ctx, ingestdb.ListEndpointsParams{
		TenantID:      tenantID,
		ApplicationID: applicationID,
		After:         after,
		MaxResults:    maxResults,
	})
	if err != nil {
		return nil, fmt.Errorf("list endpoints: %w", err)
	}
	eps := make([]Endpoint, len(rows))
	for i, row := range rows {
		eps[i] = Endpoint(row)
	}
	return eps, nil
}
