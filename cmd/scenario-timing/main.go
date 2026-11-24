// Command scenario-timing compares the timing statistics of two runs of the
// scenario, the base's and the candidate's, run on the same machine at the
// same time, each within its margin (docs/testing.md, "Timing margins"). It
// prints the comparison as a Markdown report, and fails if any statistic is
// beyond its margin or has no samples to compare, naming each: with exit
// status 1, and 2 when it can't compare at all. Run it through
// `make scenario-timing`.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/madskrogh/herald/internal/scenario"
)

// errFailed is a comparison that ran and failed.
var errFailed = errors.New("the timing comparison failed")

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "scenario-timing:", err)
		if errors.Is(err, errFailed) {
			os.Exit(1)
		}
		os.Exit(2)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("scenario-timing", flag.ContinueOnError)
	baseDir := fs.String("base", "", "the `dir` the base run wrote its statistics.json to")
	candidateDir := fs.String("candidate", "", "the `dir` the candidate run wrote its statistics.json to")
	baseLabel := fs.String("base-label", "base", "which build the base is, such as its commit")
	candidateLabel := fs.String("candidate-label", "candidate", "which build the candidate is")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *baseDir == "" || *candidateDir == "" {
		return errors.New("set -base and -candidate")
	}
	base, err := read(*baseDir, *baseLabel)
	if err != nil {
		return err
	}
	candidate, err := read(*candidateDir, *candidateLabel)
	if err != nil {
		return err
	}
	rows := scenario.CompareTiming(base.Statistics.Timing, candidate.Statistics.Timing, scenario.TimingMargins)
	if err := scenario.WriteTimingReport(out, base, candidate, rows); err != nil {
		return err
	}
	var failed []string
	for _, r := range rows {
		if !r.Within {
			failed = append(failed, r.String())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", errFailed, strings.Join(failed, "; "))
	}
	return nil
}

func read(dir, label string) (scenario.TimingRun, error) {
	r := scenario.TimingRun{Label: label}
	file := filepath.Join(dir, "statistics.json")
	data, err := os.ReadFile(file)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r.Statistics); err != nil {
		return r, fmt.Errorf("%s: %w", file, err)
	}
	return r, nil
}
