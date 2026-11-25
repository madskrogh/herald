package scenario_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

func TestCompareCounts(t *testing.T) {
	t.Parallel()
	traffic := scenario.TrafficFile{File: "traffic.json", SHA256: "aa"}
	baseline := scenario.Counts{Traffic: traffic, Counts: map[string]int{
		"attempt succeeded":  3,
		"attempt pending":    2,
		"handoff handed_off": 2,
	}}
	tests := []struct {
		name string
		got  scenario.Counts
		want []scenario.Difference
	}{
		{
			name: "the same",
			got:  baseline,
		},
		{
			name: "a count that changed, one gone and one new",
			got: scenario.Counts{Traffic: traffic, Counts: map[string]int{
				"attempt succeeded": 3,
				"attempt pending":   4,
				"attempt failed":    1,
			}},
			want: []scenario.Difference{
				{Field: "spans: attempt failed", Want: "(none)", Got: "1"},
				{Field: "spans: attempt pending", Want: "2", Got: "4"},
				{Field: "spans: handoff handed_off", Want: "2", Got: "(none)"},
			},
		},
		{
			// A traffic file changed since the baseline was recorded comes
			// first.
			name: "another traffic file",
			got: scenario.Counts{
				Traffic: scenario.TrafficFile{File: "traffic.json", SHA256: "bb"},
				Counts:  baseline.Counts,
			},
			want: []scenario.Difference{
				{Field: "traffic sha256", Want: "aa", Got: "bb"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scenario.CompareCounts(baseline, tt.got)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("CompareCounts differs (-want +got):\n%s", diff)
			}
		})
	}
}
