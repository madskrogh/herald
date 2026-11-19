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

var (
	update = flag.Bool("update", false, "record a new baseline instead of comparing with it")
	seed   = flag.Uint64("seed", 0, "with -update, the seed to generate the scenario from; 0 keeps the baseline's")
)

// baselineFile is the committed baseline.
const baselineFile = "testdata/baseline.json"

// runTimeout bounds a whole run. A run takes about five minutes: the
// longest Deliveries run the whole retry schedule, about 90 seconds on the
// local stack, before the phases with faults start, and Deliveries take a
// few minutes while Postgres is slowed.
const runTimeout = 10 * time.Minute

// TestScenario runs the scenario against the stack and compares what its
// Endpoints received with the baseline, or records a new baseline with
// -update. It restarts herald-dispatcher, pauses NATS and slows Postgres, so
// nothing else may use the stack meanwhile, and for the same reason it
// doesn't run in parallel. Run it with make scenario-compare or make
// scenario-record.
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
	case errors.Is(err, fs.ErrNotExist) && *update:
		baseline.Seed = 1
	case err != nil:
		t.Fatal(err)
	}
	s := baseline.Seed
	if *update && *seed != 0 {
		s = *seed
	}

	ctx, cancel := context.WithTimeout(t.Context(), runTimeout)
	defer cancel()
	stack, err := e2e.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	obs, err := scenario.Run(ctx, stack, scenario.Compose{File: composeFile}, scenario.Generate(s), logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := scenario.NewRecording(obs)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}

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
		t.Logf("recorded %s from seed %d", baselineFile, s)
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
	return r, nil
}
