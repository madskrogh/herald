package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

var (
	lag50 = scenario.TimingKey{Statistic: "handoff lag", Percentile: scenario.P50}
	lag95 = scenario.TimingKey{Statistic: "handoff lag", Percentile: scenario.P95}
	lat50 = scenario.TimingKey{Statistic: "Attempt latency", Percentile: scenario.P50}
	lat95 = scenario.TimingKey{Statistic: "Attempt latency", Percentile: scenario.P95}
)

// ms returns a pointer to v, a value in milliseconds.
func ms(v int64) *int64 { return &v }

func TestCompareTiming(t *testing.T) {
	t.Parallel()
	// Each margin is 50% of the base's value plus 10 ms: 60 ms either way
	// of a base of 100 ms, and 210 ms of a base of 400 ms.
	margins := map[scenario.TimingKey]scenario.Margin{
		lag50: {Percent: 50, Ms: 10},
		lag95: {Percent: 50, Ms: 10},
		lat50: {Percent: 50, Ms: 10},
		lat95: {Percent: 50, Ms: 10},
	}
	baseRun := map[string]scenario.Distribution{
		"handoff lag":     {N: 84, P50Ms: 100, P95Ms: 400},
		"Attempt latency": {N: 120, P50Ms: 100, P95Ms: 400},
	}
	tests := []struct {
		name string
		// base is baseRun unless set.
		base      map[string]scenario.Distribution
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
				{Key: lat50, Base: ms(100), Candidate: ms(40), Margin: 60, Within: true},
				{Key: lat95, Base: ms(400), Candidate: ms(610), Margin: 210, Within: true},
				{Key: lag50, Base: ms(100), Candidate: ms(160), Margin: 60, Within: true},
				{Key: lag95, Base: ms(400), Candidate: ms(190), Margin: 210, Within: true},
			},
		},
		{
			name: "beyond a margin, slower and faster",
			candidate: map[string]scenario.Distribution{
				"handoff lag":     {N: 84, P50Ms: 161, P95Ms: 400},
				"Attempt latency": {N: 120, P50Ms: 100, P95Ms: 189},
			},
			want: []scenario.TimingRow{
				{Key: lat50, Base: ms(100), Candidate: ms(100), Margin: 60, Within: true},
				{Key: lat95, Base: ms(400), Candidate: ms(189), Margin: 210},
				{Key: lag50, Base: ms(100), Candidate: ms(161), Margin: 60},
				{Key: lag95, Base: ms(400), Candidate: ms(400), Margin: 210, Within: true},
			},
		},
		{
			// A handoff can start before the span of the call that sent
			// its Message ends, so lag can be negative; its margin is
			// still 50% of its size plus 10 ms.
			name: "a negative value",
			base: map[string]scenario.Distribution{
				"handoff lag":     {N: 84, P50Ms: 100, P95Ms: 400},
				"Attempt latency": {N: 120, P50Ms: -100, P95Ms: -40},
			},
			candidate: map[string]scenario.Distribution{
				"handoff lag":     {N: 84, P50Ms: 100, P95Ms: 400},
				"Attempt latency": {N: 120, P50Ms: 100, P95Ms: -70},
			},
			want: []scenario.TimingRow{
				{Key: lat50, Base: ms(-100), Candidate: ms(100), Margin: 60},
				{Key: lat95, Base: ms(-40), Candidate: ms(-70), Margin: 30, Within: true},
				{Key: lag50, Base: ms(100), Candidate: ms(100), Margin: 60, Within: true},
				{Key: lag95, Base: ms(400), Candidate: ms(400), Margin: 210, Within: true},
			},
		},
		{
			// A statistic one run has no samples for can't be compared.
			name: "a statistic the candidate doesn't have",
			candidate: map[string]scenario.Distribution{
				"handoff lag": {N: 84, P50Ms: 100, P95Ms: 400},
			},
			want: []scenario.TimingRow{
				{Key: lat50, Base: ms(100), Margin: 60},
				{Key: lat95, Base: ms(400), Margin: 210},
				{Key: lag50, Base: ms(100), Candidate: ms(100), Margin: 60, Within: true},
				{Key: lag95, Base: ms(400), Candidate: ms(400), Margin: 210, Within: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := baseRun
			if tt.base != nil {
				b = tt.base
			}
			got := scenario.CompareTiming(b, tt.candidate, margins)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("CompareTiming differs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTimingRowString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		row  scenario.TimingRow
		want string
	}{
		{
			name: "both runs",
			row:  scenario.TimingRow{Key: lag95, Base: ms(400), Candidate: ms(700), Margin: 210},
			want: "handoff lag p95: base 400, candidate 700, margin ± 210 (ms): beyond the margin",
		},
		{
			name: "no base",
			row:  scenario.TimingRow{Key: lag95, Candidate: ms(700)},
			want: "handoff lag p95: base (none), candidate 700, margin (none) (ms): no samples to compare",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.row.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWriteTimingReport checks the report against
// testdata/timing-report.golden; -update rewrites it.
func TestWriteTimingReport(t *testing.T) {
	t.Parallel()
	baseRun := scenario.TimingRun{Label: "main at 15a7e84", Statistics: scenario.Statistics{
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
		{Key: lat50, Base: ms(3), Margin: 20},
		{Key: lag50, Base: ms(100), Candidate: ms(161), Margin: 60},
		{Key: lag95, Base: ms(400), Candidate: ms(400), Margin: 210, Within: true},
	}
	var b strings.Builder
	if err := scenario.WriteTimingReport(&b, baseRun, candidate, rows); err != nil {
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
