package scenario_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

func TestGenerateIsRepeatable(t *testing.T) {
	t.Parallel()
	if diff := cmp.Diff(scenario.Generate(7), scenario.Generate(7)); diff != "" {
		t.Errorf("Generate(7) twice (-first +second):\n%s", diff)
	}
	if cmp.Equal(scenario.Generate(7), scenario.Generate(8)) {
		t.Error("Generate(7) and Generate(8) are the same scenario")
	}
}

// TestGenerateWritesTraffic checks that the generator's traffic, written as
// a traffic file, reads back as the same traffic.
func TestGenerateWritesTraffic(t *testing.T) {
	t.Parallel()
	want := scenario.Generate(7)
	path := filepath.Join(t.TempDir(), "traffic.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := scenario.WriteTraffic(f, want); err != nil {
		t.Fatalf("WriteTraffic: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := scenario.ReadTraffic(path)
	if err != nil {
		t.Fatalf("ReadTraffic: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Generate(7) read back (-want +got):\n%s", diff)
	}
}

// TestGenerateCoversEverything checks the generator's traffic, for many
// seeds, against the rules the committed traffic file is held to.
func TestGenerateCoversEverything(t *testing.T) {
	t.Parallel()
	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			checkCoverage(t, scenario.Generate(seed))
		})
	}
}
