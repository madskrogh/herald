// Command scenario-timing writes the report of a run of make
// scenario-timing: the candidate's behaviour comparison and invariants, as
// its run found them, and the comparison of its timing statistics with the
// base's, run on the same machine at the same time, each within its margin
// (docs/testing.md, "Timing margins"). It prints the report as Markdown,
// and writes it whatever the runs found, saying what each couldn't do. It
// fails if any statistic is beyond its margin or has no samples to
// compare, naming each: with exit status 1, and 2 when it can't compare at
// all. The candidate's own failures fail its run, not this. Run it through
// `make scenario-timing`.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
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
	flags := flag.NewFlagSet("scenario-timing", flag.ContinueOnError)
	baseDir := flags.String("base", "", "the `dir` the base run wrote its statistics.json to")
	candidateDir := flags.String("candidate", "", "the `dir` the candidate run wrote its statistics.json and result.json to")
	baseLabel := flags.String("base-label", "base", "which build the base is, such as its commit")
	candidateLabel := flags.String("candidate-label", "candidate", "which build the candidate is")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baseDir == "" || *candidateDir == "" {
		return errors.New("set -base and -candidate")
	}
	result := readResult(*candidateDir)
	timing := &scenario.TimingReport{
		Base:      scenario.TimingRun{Label: *baseLabel},
		Candidate: scenario.TimingRun{Label: *candidateLabel},
	}
	// A run without statistics stopped before it wrote them: the report
	// still says what the candidate found.
	compareErr := errors.Join(
		readJSON(filepath.Join(*baseDir, "statistics.json"), &timing.Base.Statistics),
		readJSON(filepath.Join(*candidateDir, "statistics.json"), &timing.Candidate.Statistics),
	)
	if compareErr != nil {
		timing.Error = compareErr.Error()
	} else {
		timing.Rows = scenario.CompareTiming(timing.Base.Statistics.Timing, timing.Candidate.Statistics.Timing, scenario.TimingMargins)
	}
	if err := scenario.WriteReport(out, scenario.Report{Candidate: result, Timing: timing}); err != nil {
		return err
	}
	if compareErr != nil {
		return compareErr
	}
	var failed []string
	for _, r := range timing.Rows {
		if !r.Within {
			failed = append(failed, r.String())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", errFailed, strings.Join(failed, "; "))
	}
	return nil
}

// readResult reads the candidate's result.json from dir. A run that wrote
// none, or one that can't be read, is reported as a run that stopped, so
// that the report still holds the timing: its test.log says why.
func readResult(dir string) scenario.Result {
	var r scenario.Result
	log := filepath.Join(filepath.Base(dir), "test.log")
	switch err := readJSON(filepath.Join(dir, "result.json"), &r); {
	case errors.Is(err, fs.ErrNotExist):
		return scenario.Result{Stopped: fmt.Sprintf("the run wrote no result: %s says why", log)}
	case err != nil:
		return scenario.Result{Stopped: fmt.Sprintf("the run's result can't be read (%v): %s says what it found", err, log)}
	}
	return r
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
