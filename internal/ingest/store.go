// Package ingest owns herald-api's ingest schema: its migrations and the
// Tenants and API keys stored in it.
package ingest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/ingest/ingestdb"
)

// apiKeyPrefix starts every API key, so that secret scanners can recognize
// a leaked one.
const apiKeyPrefix = "herald_"

// foreignKeyViolation is the Postgres SQLSTATE for an insert that references
// a row that doesn't exist.
const foreignKeyViolation = "23503"

// ErrNotFound is returned when an API key or a Tenant doesn't exist.
var ErrNotFound = errors.New("not found")

// Tenant is an organization that uses Herald to send webhooks.
type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Store reads and writes Tenants and API keys in the ingest schema.
type Store struct {
	q *ingestdb.Queries
}

// NewStore returns a Store that uses pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{q: ingestdb.New(pool)}
}

// CreateTenant creates a Tenant with the given name.
func (s *Store) CreateTenant(ctx context.Context, name string) (Tenant, error) {
	row, err := s.q.CreateTenant(ctx, name)
	if err != nil {
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return Tenant(row), nil
}

// CreateAPIKey creates an API key for the Tenant and returns it. Only its
// hash is stored, so this is the only time the key is available.
func (s *Store) CreateAPIKey(ctx context.Context, tenantID uuid.UUID) (string, error) {
	key := apiKeyPrefix + rand.Text()
	err := s.q.CreateAPIKey(ctx, ingestdb.CreateAPIKeyParams{
		TenantID: tenantID,
		KeyHash:  hashAPIKey(key),
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == foreignKeyViolation {
		return "", fmt.Errorf("create api key: tenant %s: %w", tenantID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("create api key for tenant %s: %w", tenantID, err)
	}
	return key, nil
}

// TenantByAPIKey returns the Tenant that key belongs to.
func (s *Store) TenantByAPIKey(ctx context.Context, key string) (Tenant, error) {
	row, err := s.q.GetTenantByAPIKeyHash(ctx, hashAPIKey(key))
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, fmt.Errorf("get tenant by api key: %w", ErrNotFound)
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("get tenant by api key: %w", err)
	}
	return Tenant(row), nil
}

// hashAPIKey returns the SHA-256 hash under which key is stored.
func hashAPIKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}
