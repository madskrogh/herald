// Package migrate applies a service's goose migrations to the Postgres schema
// it owns. Each service runs it as a one-shot step before it starts, never
// from the service itself.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
)

// Schema is the Postgres schema a service owns.
type Schema struct {
	// Name is the schema's name.
	Name string
	// Create is the literal statement that creates the schema if it doesn't
	// exist, such as "CREATE SCHEMA IF NOT EXISTS ingest". An identifier
	// can't be a query parameter, and SQL is never built from values in Go
	// (ADR 0005), so each service spells out its own.
	Create string
}

// Run applies the migrations in fsys that haven't been applied to schema, in
// the database at databaseURL.
func Run(ctx context.Context, databaseURL string, schema Schema, fsys fs.FS, logger *slog.Logger) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Overlapping runs (a retried Job, overlapping deploys) take turns: each
	// holds a session-level advisory lock on a connection of its own for the
	// whole run, so the next one finds the migrations applied. Closing db ends
	// the session, which releases the lock. Each schema has its own lock, so
	// the services' migrate steps don't wait for each other.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext($1))", "herald."+schema.Name+".migrate"); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}

	// goose keeps its version table inside the schema, so that each service's
	// migration history lives with the schema it describes. The schema has to
	// exist before goose can create that table.
	if _, err := conn.ExecContext(ctx, schema.Create); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName(schema.Name+".goose_db_version"),
	)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		logger.InfoContext(ctx, "applied migration", "version", r.Source.Version, "duration", r.Duration)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}
	logger.InfoContext(ctx, "schema up to date", "version", version)
	return nil
}
