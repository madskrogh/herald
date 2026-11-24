package scenario_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestHarnessDoesNotImportHerald checks that the harness, its tests
// included, imports none of the code that writes Herald's Postgres: it reads
// the columns in the column list with queries of its own, so a build is held
// to the list, not to its own stores (ADR 0007's "The harness").
func TestHarnessDoesNotImportHerald(t *testing.T) {
	t.Parallel()

	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-test",
		"-f", "{{.ImportPath}}", ".", "../e2e").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "github.com/madskrogh/herald/internal/e2e") {
		t.Fatalf("go list didn't list the harness's packages: %q", deps)
	}
	for _, pkg := range []string{
		"github.com/madskrogh/herald/internal/ingest",
		"github.com/madskrogh/herald/internal/delivery",
	} {
		if slices.Contains(deps, pkg) {
			t.Errorf("the harness imports %s", pkg)
		}
	}
}
