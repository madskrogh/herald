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
	"testing"
	"time"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/scenario"
)

var update = flag.Bool("update", false, "record a new baseline instead of comparing with it")

const (
	// baselineFile is the committed baseline.
	baselineFile = "testdata/baseline.json"
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
// Endpoints received with the baseline, or records a new baseline with
// -update. Either way, it then checks the scenario invariants. It restarts
// herald-dispatcher, pauses NATS and slows Postgres, so nothing else may use
// the stack meanwhile, and for the same reason it doesn't run in parallel.
// Run it with make scenario-compare or make scenario-record.
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
	composeFile := os.Getenv("HERALD_SCENARIO_COMPOSE_FILE")
	if composeFile == "" {
		t.Fatal("HERALD_SCENARIO_COMPOSE_FILE is not set")
	}

	baseline, err := readBaseline()
	switch {
	case (errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNoTrafficFile)) && *update:
		baseline.Traffic.File = trafficFile
	case err != nil:
		t.Fatal(err)
	}
	// The baseline's traffic file, as it is now: if it changed since the
	// baseline was recorded, the comparison says so.
	s, traffic, err := scenario.ReadTraffic(filepath.Dir(baselineFile), baseline.Traffic.File)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), runTimeout)
	defer cancel()
	stack, err := e2e.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	idle, err := scenario.IdleGoroutines(ctx, stack, logger)
	if err != nil {
		t.Fatalf("IdleGoroutines: %v", err)
	}
	obs, err := scenario.Run(ctx, stack, scenario.Compose{File: composeFile}, s, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := scenario.NewRecording(traffic, obs)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}
	// Checked once the run is compared or recorded, whichever this is.
	defer func() {
		violations, err := scenario.CheckInvariants(ctx, stack, obs, idle)
		if err != nil {
			t.Fatalf("CheckInvariants: %v", err)
		}
		for _, v := range violations {
			t.Errorf("scenario invariant: %v", v)
		}
	}()

	if *update {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(baselineFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselineFile, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s from %s", baselineFile, traffic.File)
		return
	}
	for _, d := range scenario.Compare(baseline, got) {
		t.Error(d)
	}
}

func readBaseline() (scenario.Recording, error) {
	var r scenario.Recording
	data, err := os.ReadFile(baselineFile)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("%s: %w", baselineFile, err)
	}
	if r.Traffic.File == "" {
		return r, fmt.Errorf("%s: %w", baselineFile, errNoTrafficFile)
	}
	return r, nil
}

// errNoTrafficFile is a baseline naming no traffic file, such as one
// recorded from a seed. Recording a new one replaces it.
var errNoTrafficFile = errors.New("names no traffic file: record a new baseline with make scenario-record")
