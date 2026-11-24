package telemetry_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/spanschema"
	"github.com/madskrogh/herald/internal/telemetry"
)

// TestSchemaMatchesTheSpec checks the span names, attribute keys and outcome
// values the code emits against the span schema in the spec. The types of
// the values are checked on every span a test records (telemetrytest).
func TestSchemaMatchesTheSpec(t *testing.T) {
	t.Parallel()
	schema, err := spanschema.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantAttrs := map[string][]string{}
	wantOutcomes := map[string][]string{}
	for name, s := range schema.Spans {
		wantAttrs[name] = s.Attributes
		wantOutcomes[name] = s.Outcomes
	}
	gotAttrs := map[string][]string{}
	for name, keys := range telemetry.Schema {
		for _, k := range keys {
			gotAttrs[name] = append(gotAttrs[name], string(k))
		}
	}
	sorted := cmp.Transformer("sorted", func(s []string) []string {
		s = slices.Clone(s)
		slices.Sort(s)
		return s
	})
	if diff := cmp.Diff(wantAttrs, gotAttrs, sorted); diff != "" {
		t.Errorf("telemetry.Schema differs from the spec's attributes of each span (-spec +code):\n%s", diff)
	}
	if diff := cmp.Diff(wantOutcomes, telemetry.Outcomes, sorted); diff != "" {
		t.Errorf("telemetry.Outcomes differs from the spec's outcomes (-spec +code):\n%s", diff)
	}
}
