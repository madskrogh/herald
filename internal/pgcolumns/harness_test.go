package pgcolumns_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/migrate"
	"github.com/madskrogh/herald/internal/pgcolumns"
	"github.com/madskrogh/herald/internal/pgtest"
)

// harnessSchema holds the schema the harness's queries are compiled against
// (internal/e2e/postgres). sqlc refuses a query that reads a column it
// doesn't have, so the harness reads only the columns the list names as
// long as this schema has no others.
const harnessSchema = "../e2e/postgres/schema"

// TestHarnessReadsOnlyTheList checks that the harness's schema has exactly
// the list's columns, of their types: none it doesn't name, and every one
// it does.
func TestHarnessReadsOnlyTheList(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	t.Parallel()
	ctx := t.Context()

	dbURL := pgtest.NewDatabase(t)
	// goose's version table goes in a schema of its own, which the check
	// doesn't read.
	schema := migrate.Schema{Name: "harness", Create: "CREATE SCHEMA IF NOT EXISTS harness"}
	if err := migrate.Run(ctx, dbURL, schema, os.DirFS(harnessSchema), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("create the harness's schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	list, err := pgcolumns.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	stored, err := pgcolumns.Read(ctx, pool, pgcolumns.Schemas(list))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(stored) == 0 {
		t.Fatalf("the harness's schema has no columns in %q", pgcolumns.Schemas(list))
	}
	for _, p := range pgcolumns.Check(list, stored) {
		t.Errorf("harness schema: %v", p)
	}
	for _, s := range pgcolumns.Unlisted(list, stored) {
		t.Errorf("harness schema: %s.%s is not in the list", s.Table, s.Name)
	}
}
