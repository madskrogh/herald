package ingest_test

import (
	"log/slog"
	"sync"
	"testing"

	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/pgtest"
)

// TestConcurrentMigrate runs the migrate step several times at once on the
// same database, as overlapping deploys or a retried Job would.
func TestConcurrentMigrate(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	dbURL := pgtest.NewDatabase(t)

	const runs = 8
	errs := make([]error, runs)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Go(func() {
			errs[i] = ingest.Migrate(t.Context(), dbURL, slog.New(slog.DiscardHandler))
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("run %d: Migrate: %v", i, err)
		}
	}
}
