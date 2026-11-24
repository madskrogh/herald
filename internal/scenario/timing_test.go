package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

func TestCompareTiming(t *testing.T) {
	t.Parallel()
	// Each margin is 50% of the base's value plus 10 ms: 60 ms either way
	// of a base of 100 ms, and 210 ms of a base of 400 ms.
	margins := map[string]scenario.Margin{
		"handoff lag p50":     {Percent: 50, Ms: 10},
		"handoff lag p95":     {Percent: 50, Ms: 10},
		"Attempt latency p50": {Percent: 50, Ms: 10},
		"Attempt latency p95": {Percent: 50, Ms: 10},
	}
	base := map[string]scenario.Distribution{
		"handoff lag":     {N: 84, P50Ms: 100, P95Ms: 400},
		"Attempt latency": {N: 120, P50Ms: 100, P95Ms: 400},
	}
	tests := []struct {
		name      string
		candidate map[string]scenario.Distribution
		want      []scenario.TimingRow
	}{
		{
			name: "within every margin, at its edges",
			candidate: map[string]scenario.Distribution{
				"handoff lag":     {N: 84, P50Ms: 160, P95Ms: 190},
				"Attempt latency": {N: 120, P50Ms: 40, P95Ms: 610},
			},
			want: []scenario.TimingRow{
				{Statistic: "Attempt latency p50", Base: "100", Candidate: "40", Margin: "± 60", Within: true},
				{Statistic: "Attempt latency p95", Base: "400", Candidate: "610", Margin: "± 210", Within: true},
				{Statistic: "handoff lag p50", Base: "100", Candidate: "160", Margin: "± 60", Within: true},
				{Statistic: "handoff lag p95", Base: "400", Candidate: "190", Margin: "± 210", Within: true},
			},
		},
		{
			name: "beyond a margin, slower and faster",
			candidate: map[string]scenario.Distribution{
				"handoff lag":     {N: 84, P50Ms: 161, P95Ms: 400},
				"Attempt latency": {N: 120, P50Ms: 100, P95Ms: 189},
			},
			want: []scenario.TimingRow{
				{Statistic: "Attempt latency p50", Base: "100", Candidate: "100", Margin: "± 60", Within: true},
				{Statistic: "Attempt latency p95", Base: "400", Candidate: "189", Margin: "± 210"},
				{Statistic: "handoff lag p50", Base: "100", Candidate: "161", Margin: "± 60"},
				{Statistic: "handoff lag p95", Base: "400", Candidate: "400", Margin: "± 210", Within: true},
			},
		},
		{
			// A statistic one run has no samples for can't be compared.
			name: "a statistic the candidate doesn't have",
			candidate: map[string]scenario.Distribution{
				"handoff lag": {N: 84, P50Ms: 100, P95Ms: 400},
			},
			want: []scenario.TimingRow{
				{Statistic: "Attempt latency p50", Base: "100", Candidate: "(none)", Margin: "± 60"},
				{Statistic: "Attempt latency p95", Base: "400", Candidate: "(none)", Margin: "± 210"},
				{Statistic: "handoff lag p50", Base: "100", Candidate: "100", Margin: "± 60", Within: true},
				{Statistic: "handoff lag p95", Base: "400", Candidate: "400", Margin: "± 210", Within: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scenario.CompareTiming(base, tt.candidate, margins)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("CompareTiming differs (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWriteTimingReport checks the report against
// testdata/timing-report.golden; -update rewrites it.
func TestWriteTimingReport(t *testing.T) {
	t.Parallel()
	base := scenario.TimingRun{Label: "main at 15a7e84", Statistics: scenario.Statistics{
		Timing: map[string]scenario.Distribution{
			"handoff lag":     {N: 84, P50Ms: 100, P95Ms: 400},
			"Attempt latency": {N: 120, P50Ms: 3, P95Ms: 9},
		},
	}}
	candidate := scenario.TimingRun{Label: "PR at 0123abc", Statistics: scenario.Statistics{
		Timing: map[string]scenario.Distribution{
			"handoff lag": {N: 84, P50Ms: 161, P95Ms: 400},
		},
	}}
	rows := []scenario.TimingRow{
		{Statistic: "Attempt latency p50", Base: "3", Candidate: "(none)", Margin: "± 20"},
		{Statistic: "handoff lag p50", Base: "100", Candidate: "161", Margin: "± 60"},
		{Statistic: "handoff lag p95", Base: "400", Candidate: "400", Margin: "± 210", Within: true},
	}
	var b strings.Builder
	if err := scenario.WriteTimingReport(&b, base, candidate, rows); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "timing-report.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), b.String()); diff != "" {
		t.Errorf("WriteTimingReport differs from %s (-want +got):\n%s", golden, diff)
	}
}
