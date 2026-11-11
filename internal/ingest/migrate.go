package ingest

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies the ingest schema's migrations that haven't been applied
// to the database at databaseURL. It is run as a one-shot step before
// herald-api starts, never by the service itself.
func Migrate(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Overlapping runs (a retried Job, overlapping deploys) take turns: each
	// holds a session-level advisory lock on a connection of its own for the
	// whole run, so the next one finds the migrations applied. Closing db ends
	// the session, which releases the lock.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext('herald.ingest.migrate'))"); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}

	// goose keeps its version table inside the schema, so that each service's
	// migration history lives with the schema it describes. The schema has to
	// exist before goose can create that table.
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS ingest"); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName("ingest.goose_db_version"),
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
