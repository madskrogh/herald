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

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
)

// Run applies the migrations in fsys that haven't been applied to schema, in
// the database at databaseURL. schema is the name of the service's schema,
// a constant of the calling package, never input.
func Run(ctx context.Context, databaseURL, schema string, fsys fs.FS, logger *slog.Logger) error {
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
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext($1))", "herald."+schema+".migrate"); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}

	// goose keeps its version table inside the schema, so that each service's
	// migration history lives with the schema it describes. The schema has to
	// exist before goose can create that table. An identifier can't be a
	// query parameter, so the name is quoted into the statement; it is a
	// constant of the calling package.
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName(schema+".goose_db_version"),
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
