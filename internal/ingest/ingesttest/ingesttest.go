// Package ingesttest provides migrated ingest databases for integration
// tests. The test package must start Postgres with pgtest.Main.
package ingesttest

import (
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/pgtest"
)

// NewDatabase creates a fresh database for t, applies the ingest migrations
// and returns its connection URL.
func NewDatabase(t *testing.T) string {
	t.Helper()
	dbURL := pgtest.NewDatabase(t)
	if err := ingest.Migrate(t.Context(), dbURL, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("ingesttest: migrate: %v", err)
	}
	return dbURL
}

// NewStore returns a Store on the database at dbURL, and the pool it uses.
// The pool is closed when t finishes.
func NewStore(t *testing.T, dbURL string) (*ingest.Store, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("ingesttest: connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return ingest.NewStore(pool), pool
}
