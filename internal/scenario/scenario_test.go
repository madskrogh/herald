package scenario_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/scenario"
)

var (
	update   = flag.Bool("update", false, "record new baselines, and rewrite golden files, instead of comparing with them")
	out      = flag.String("out", "", "write the run's statistics to `dir`/statistics.json, for the timing comparison, and what it found to dir/result.json and dir/report.md")
	baseOnly = flag.Bool("base", false, "run as the base build of the timing comparison: only run the scenario and write its statistics, neither comparing with the baselines nor checking the invariants")
)

const (
	// baselineFile is the committed baseline of what the Endpoints
	// received, and countsFile of the span counts.
	baselineFile = "testdata/baseline.json"
	countsFile   = "testdata/counts.json"
	// trafficFile is the committed traffic file, from the baseline's
	// directory: what a new baseline is recorded from when there is none.
	trafficFile = "traffic.json"
)

// runTimeout bounds a whole run and the invariants checked after it. A run
// takes about five minutes: the longest Deliveries run the whole retry
// schedule, about 90 seconds on the local stack, before the phases with
// faults start, and Deliveries take a few minutes while Postgres is slowed.
// The invariants take seconds, and up to two minutes each to wait for spans
// and goroutines to settle.
const runTimeout = 15 * time.Minute

// TestScenario runs the scenario against the stack and compares what its
// Endpoints received, and its span counts, with the baselines, or records
// new baselines with -update. Either way, it then checks the scenario
// invariants. With -out, it also writes the run's statistics, and what it
// found, as far as it got, and its report; with -base it writes only the
// statistics. It restarts herald-dispatcher, pauses NATS and
// slows Postgres, so nothing else may use the stack meanwhile, and for the
// same reason it doesn't run in parallel. Run it with make
// scenario-compare, make scenario-record or make scenario-timing.
func TestScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	cfg, ok, err := e2e.ConfigFromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no stack: run with make scenario-compare")
	}
	// What the run found, written once it ends however it ends; stop ends
	// it, saying why.
	var result scenario.Result
	if *out != "" && !*baseOnly {
		// An earlier run's, so that a run the test timeout ends, which
		// writes nothing, isn't reported with its findings.
		for _, f := range []string{"result.json", "report.md"} {
			if err := os.Remove(filepath.Join(*out, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
		}
		defer writeResult(t, *out, &result)
	}
	// The first reason is the one reported: a later one, such as the
	// invariants failing after the comparison stopped, follows from it.
	stop := func(format string, args ...any) {
		t.Helper()
		reason := fmt.Sprintf(format, args...)
		if result.Stopped == "" {
			result.Stopped = reason
		}
		t.Fatal(reason)
	}
	composeFile := os.Getenv("HERALD_SCENARIO_COMPOSE_FILE")
	if composeFile == "" {
		stop("HERALD_SCENARIO_COMPOSE_FILE is not set")
	}

	baseline, err := readBaseline()
	switch {
	case (errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNoTrafficFile)) && *update:
		baseline.Traffic.File = trafficFile
	case err != nil:
		stop("%v", err)
	}
	// The baseline's traffic file, as it is now: if it changed since the
	// baseline was recorded, the comparison says so.
	s, traffic, err := scenario.ReadTraffic(filepath.Dir(baselineFile), baseline.Traffic.File)
	if err != nil {
		stop("%v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), runTimeout)
	defer cancel()
	stack, err := e2e.Connect(ctx, cfg)
	if err != nil {
		stop("%v", err)
	}
	defer stack.Close()
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	idle, err := scenario.IdleGoroutines(ctx, stack, logger)
	if err != nil {
		stop("IdleGoroutines: %v", err)
	}
	obs, err := scenario.Run(ctx, stack, scenario.Compose{File: composeFile}, s, logger)
	if err != nil {
		stop("Run: %v", err)
	}
	spans, err := scenario.SettledSpans(ctx, stack, obs)
	if err != nil {
		stop("SettledSpans: %v", err)
	}
	stats := scenario.NewStatistics(obs, spans)
	if *out != "" {
		if err := writeJSON(filepath.Join(*out, "statistics.json"), stats); err != nil {
			stop("%v", err)
		}
	}
	if *baseOnly {
		return
	}
	got, err := scenario.NewRecording(traffic, obs)
	if err != nil {
		stop("NewRecording: %v", err)
	}
	counts := scenario.Counts{Traffic: traffic, Counts: stats.Counts}
	// Checked once the run is compared or recorded, whichever this is.
	defer func() {
		violations, err := scenario.CheckInvariants(ctx, stack, obs, spans, idle)
		if err != nil {
			stop("CheckInvariants: %v", err)
		}
		result.Invariants = &scenario.Invariants{Violations: violations}
		for _, v := range violations {
			t.Errorf("scenario invariant: %v", v)
		}
	}()

	if *update {
		if err := writeJSON(baselineFile, got); err != nil {
			stop("%v", err)
		}
		if err := writeJSON(countsFile, counts); err != nil {
			stop("%v", err)
		}
		result.Recorded = true
		t.Logf("recorded %s and %s from %s", baselineFile, countsFile, traffic.File)
		return
	}
	var wantCounts scenario.Counts
	if err := readJSON(countsFile, &wantCounts); err != nil {
		stop("%v", err)
	}
	result.Behaviour = &scenario.Differences{
		Edge:   scenario.Compare(baseline, got),
		Counts: scenario.CompareCounts(wantCounts, counts),
	}
	for _, d := range result.Behaviour.Edge {
		t.Error(d)
	}
	for _, d := range result.Behaviour.Counts {
		t.Errorf("%s: %v", countsFile, d)
	}
}

// writeResult writes what the run found to dir/result.json, for the timing
// comparison's report, and its report to dir/report.md.
func writeResult(t *testing.T, dir string, result *scenario.Result) {
	t.Helper()
	// A run that ended without stop, as on a panic or a helper's t.Fatal,
	// still says it stopped.
	unfinished := result.Invariants == nil || (result.Behaviour == nil && !result.Recorded)
	if result.Stopped == "" && unfinished {
		result.Stopped = "the run stopped before it finished: test.log says why"
	}
	if err := writeJSON(filepath.Join(dir, "result.json"), result); err != nil {
		t.Error(err)
		return
	}
	var b strings.Builder
	if err := scenario.WriteReport(&b, scenario.Report{Candidate: *result}); err != nil {
		t.Error(err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(b.String()), 0o644); err != nil {
		t.Error(err)
	}
}

func readBaseline() (scenario.Recording, error) {
	var r scenario.Recording
	if err := readJSON(baselineFile, &r); err != nil {
		return r, err
	}
	if r.Traffic.File == "" {
		return r, fmt.Errorf("%s: %w", baselineFile, errNoTrafficFile)
	}
	return r, nil
}

func readJSON(file string, v any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

// writeJSON writes v to file as indented JSON, making its directory.
func writeJSON(file string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

// errNoTrafficFile is a baseline naming no traffic file, such as one
// recorded from a seed. Recording a new one replaces it.
var errNoTrafficFile = errors.New("names no traffic file: record a new baseline with make scenario-record")
