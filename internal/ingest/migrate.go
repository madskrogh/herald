package ingest

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/madskrogh/herald/internal/migrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies the ingest schema's migrations that haven't been applied
// to the database at databaseURL. It is run as a one-shot step before
// herald-api starts, never by the service itself.
func Migrate(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	schema := migrate.Schema{Name: "ingest", Create: "CREATE SCHEMA IF NOT EXISTS ingest"}
	return migrate.Run(ctx, databaseURL, schema, fsys, logger)
}
