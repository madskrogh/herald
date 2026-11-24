package pgcolumns_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/pgcolumns"
	"github.com/madskrogh/herald/internal/pgtest"
)

// harnessSchema is the schema the harness's queries are compiled against
// (internal/e2e/postgres). sqlc refuses a query that reads a column it
// doesn't have, so the harness reads only the columns the list names as
// long as this schema has no others.
const harnessSchema = "../e2e/postgres/schema.sql"

// TestHarnessReadsOnlyTheList checks that the harness's schema has exactly
// the list's columns, of their types: none it doesn't name, and every one
// it does.
func TestHarnessReadsOnlyTheList(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	t.Parallel()
	ctx := t.Context()

	ddl, err := os.ReadFile(harnessSchema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, pgtest.NewDatabase(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	// The schema file is fixed DDL, run as the migrations are; nothing in it
	// comes from Go.
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatalf("create the harness's schema: %v", err)
	}

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
	listed := map[string]bool{}
	for _, c := range list {
		listed[c.Table+"."+c.Name] = true
	}
	for _, s := range stored {
		if !listed[s.Table+"."+s.Name] {
			t.Errorf("harness schema: %s.%s is not in the list", s.Table, s.Name)
		}
	}
}
