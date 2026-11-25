package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

// TestWriteReport checks the report of each kind of run against its golden
// file in testdata; -update rewrites them.
func TestWriteReport(t *testing.T) {
	t.Parallel()
	baseRun := scenario.TimingRun{Label: "main at 15a7e84", Statistics: scenario.Statistics{
		Timing: map[string]scenario.Distribution{
			"handoff lag":     {N: 84, P50Ms: 100, P95Ms: 400},
			"Attempt latency": {N: 120, P50Ms: 3, P95Ms: 9},
		},
	}}
	tests := []struct {
		name   string
		report scenario.Report
	}{
		{
			name: "report-pass",
			report: scenario.Report{
				Candidate: scenario.Result{
					Behaviour:  &scenario.Differences{},
					Invariants: &scenario.Invariants{},
				},
				Timing: &scenario.TimingReport{
					Base: baseRun,
					Candidate: scenario.TimingRun{Label: "PR at 0123abc", Statistics: scenario.Statistics{
						Timing: map[string]scenario.Distribution{
							"handoff lag":     {N: 84, P50Ms: 110, P95Ms: 420},
							"Attempt latency": {N: 120, P50Ms: 3, P95Ms: 8},
						},
					}},
					Rows: []scenario.TimingRow{
						{Key: lat50, Base: ms(3), Candidate: ms(3), Margin: 20, Within: true},
						{Key: lag50, Base: ms(100), Candidate: ms(110), Margin: 60, Within: true},
						{Key: lag95, Base: ms(400), Candidate: ms(420), Margin: 210, Within: true},
					},
				},
			},
		},
		{
			name: "report-fail",
			report: scenario.Report{
				Candidate: scenario.Result{
					Behaviour: &scenario.Differences{
						Edge: []scenario.Difference{
							{Delivery: "m007 to orders/ep-2", Field: "attempt 2: header webhook-id", Want: "msg_1", Got: "msg_2"},
						},
						Counts: []scenario.Difference{
							{Field: "spans: delivery.attempt failure", Want: "40", Got: "41"},
						},
					},
					Invariants: &scenario.Invariants{Violations: []scenario.Violation{
						{Rule: "fan-out", Subject: "m012 to billing/ep-1", Detail: "received, but the Endpoint's filter doesn't match the Message"},
						{Rule: "fan-out", Subject: "m013 to billing/ep-1", Detail: "received, but the Endpoint's filter doesn't match the Message"},
						{Rule: "goroutines", Subject: "herald-dispatcher", Detail: "2 more goroutines than idle"},
						// A rule the report doesn't know of still gets
						// its row.
						{Rule: "ordering", Subject: "m020 to orders/ep-1", Detail: "attempt 2 before attempt 1"},
					}},
				},
				Timing: &scenario.TimingReport{
					Base: baseRun,
					Candidate: scenario.TimingRun{Label: "PR at 0123abc", Statistics: scenario.Statistics{
						Timing: map[string]scenario.Distribution{
							"handoff lag": {N: 84, P50Ms: 161, P95Ms: 400},
						},
					}},
					Rows: []scenario.TimingRow{
						{Key: lat50, Base: ms(3), Margin: 20},
						{Key: lag50, Base: ms(100), Candidate: ms(161), Margin: 60},
						{Key: lag95, Base: ms(400), Candidate: ms(400), Margin: 210, Within: true},
					},
				},
			},
		},
		{
			// make scenario-compare's report, for a run that stopped
			// before it compared anything: no timing section.
			name: "report-stopped",
			report: scenario.Report{
				Candidate: scenario.Result{Stopped: "Run: phase 2: pause NATS: exit status 1"},
			},
		},
		{
			// make scenario-timing's report, when the timing comparison
			// couldn't run.
			name: "report-no-timing",
			report: scenario.Report{
				Candidate: scenario.Result{Recorded: true, Invariants: &scenario.Invariants{}},
				Timing: &scenario.TimingReport{
					Base:      scenario.TimingRun{Label: "main at 15a7e84"},
					Candidate: scenario.TimingRun{Label: "PR at 0123abc"},
					Error:     "open candidate/statistics.json: no such file or directory",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var b strings.Builder
			if err := scenario.WriteReport(&b, tt.report); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tt.name+".golden")
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
				t.Errorf("WriteReport differs from %s (-want +got):\n%s", golden, diff)
			}
		})
	}
}
