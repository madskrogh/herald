package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statistics is a run's statistics.json with handoff lag's p50 and p95,
// and every other timed statistic the margins name.
func statistics(lagP50 string) string {
	return `{"timing": {
		"handoff lag": {"n": 84, "p50_ms": ` + lagP50 + `, "p95_ms": 400},
		"Attempt latency": {"n": 120, "p50_ms": 3, "p95_ms": 9},
		"retry lateness": {"n": 60, "p50_ms": 440, "p95_ms": 500}
	}}`
}

const candidateResult = `{
	"behaviour": {"edge": [{"delivery": "m007 to orders/ep-2", "field": "state", "want": "delivered", "got": "failed"}], "counts": []},
	"invariants": {"violations": [{"rule": "fan-out", "subject": "m012 to billing/ep-1", "detail": "received"}]}
}`

func TestRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// files are written under the test's directory, by path.
		files map[string]string
		// failed says the timing comparison ran and failed; broken that
		// it couldn't run.
		failed, broken bool
		// report holds what the report must hold, each in order.
		report []string
	}{
		{
			name: "the candidate's result beside the timing",
			files: map[string]string{
				"base/statistics.json":      statistics("100"),
				"candidate/statistics.json": statistics("110"),
				"candidate/result.json":     candidateResult,
			},
			report: []string{
				"- Behaviour comparison: **1 difference**",
				"- Timing comparison: every statistic within its margin",
				"m007 to orders/ep-2: state: want delivered, got failed",
				"fan-out: m012 to billing/ep-1: received",
				"- Base: main at 15a7e84\n- Candidate: PR at 0123abc",
				"| handoff lag p50 | 100 | 110 | ± 60 | within |",
			},
		},
		{
			name: "a statistic beyond its margin",
			files: map[string]string{
				"base/statistics.json":      statistics("100"),
				"candidate/statistics.json": statistics("161"),
				"candidate/result.json":     candidateResult,
			},
			failed: true,
			report: []string{"m007 to orders/ep-2", "| handoff lag p50 | 100 | 161 | ± 60 | **beyond the margin** |"},
		},
		{
			// A candidate that stopped before its statistics: the report
			// still says what it found.
			name: "a candidate without statistics",
			files: map[string]string{
				"base/statistics.json":  statistics("100"),
				"candidate/result.json": `{"stopped": "Run: phase 2: pause NATS: exit status 1"}`,
			},
			broken: true,
			report: []string{
				"Run: phase 2: pause NATS: exit status 1",
				"- Timing comparison: **couldn't compare**",
				"candidate/statistics.json",
			},
		},
		{
			name: "a result that can't be read",
			files: map[string]string{
				"base/statistics.json":      statistics("100"),
				"candidate/statistics.json": statistics("100"),
				"candidate/result.json":     `{"behaviour": `,
			},
			report: []string{
				"result can't be read",
				"candidate/test.log",
				"| handoff lag p50 | 100 | 100 | ± 60 | within |",
			},
		},
		{
			name: "a candidate without a result",
			files: map[string]string{
				"base/statistics.json":      statistics("100"),
				"candidate/statistics.json": statistics("100"),
			},
			report: []string{
				"wrote no result",
				"candidate/test.log",
				"- Behaviour comparison: not compared",
				"| handoff lag p50 | 100 | 100 | ± 60 | within |",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for path, content := range tt.files {
				file := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out strings.Builder
			err := run([]string{
				"-base", filepath.Join(dir, "base"), "-candidate", filepath.Join(dir, "candidate"),
				"-base-label", "main at 15a7e84", "-candidate-label", "PR at 0123abc",
			}, &out)
			switch {
			case tt.failed && !errors.Is(err, errFailed):
				t.Errorf("run: err = %v, want %v", err, errFailed)
			case tt.broken && (err == nil || errors.Is(err, errFailed)):
				t.Errorf("run: err = %v, want an error that isn't %v", err, errFailed)
			case !tt.failed && !tt.broken && err != nil:
				t.Errorf("run: %v", err)
			}
			rest := out.String()
			for _, want := range tt.report {
				i := strings.Index(rest, want)
				if i < 0 {
					t.Fatalf("report doesn't hold %q after what came before it:\n%s", want, out.String())
				}
				rest = rest[i+len(want):]
			}
		})
	}
}
