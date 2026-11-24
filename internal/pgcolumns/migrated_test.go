package pgcolumns_test

import (
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/pgcolumns"
	"github.com/madskrogh/herald/internal/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// TestMigratedDatabaseKeepsTheList is R-84's and R-85's check: Herald's
// migrations, run on an empty database, give every column the list names,
// and let the scenario write its rows.
func TestMigratedDatabaseKeepsTheList(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	t.Parallel()
	ctx := t.Context()

	dbURL := pgtest.NewDatabase(t)
	logger := slog.New(slog.DiscardHandler)
	if err := ingest.Migrate(ctx, dbURL, logger); err != nil {
		t.Fatalf("migrate ingest: %v", err)
	}
	if err := delivery.Migrate(ctx, dbURL, logger); err != nil {
		t.Fatalf("migrate delivery: %v", err)
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
	for _, p := range pgcolumns.Check(list, stored) {
		t.Errorf("migrated database: %v", p)
	}

	// Positive control: Check finds a problem for each listed column when
	// none is stored, so the list was read and the check can fire.
	if got, want := len(pgcolumns.Check(list, nil)), len(list); got != want || want == 0 {
		t.Errorf("Check on no columns found %d problems, want one for each of the list's %d columns", got, want)
	}
}
