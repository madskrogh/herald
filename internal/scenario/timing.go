package scenario

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Margin is how far a timing statistic of the candidate may be from the
// base's, either way: Percent of the base's value, plus Ms.
type Margin struct {
	Percent int64
	Ms      int64
}

// TimingRow is one timing statistic of both runs, in milliseconds.
type TimingRow struct {
	// Statistic names it, such as "handoff lag p95".
	Statistic string
	// Base and Candidate are its value in each run, or (none) for a run
	// with no samples for it.
	Base, Candidate string
	// Margin is how far apart they may be, such as "± 60".
	Margin string
	// Within says the candidate's is within the margin of the base's.
	Within bool
}

// CompareTiming compares each timing statistic that margins names, of the
// candidate with the base's, run on the same machine at the same time, and
// returns them by name. A statistic is a timing statistic of Statistics
// and a percentile, such as "handoff lag p95".
func CompareTiming(base, candidate map[string]Distribution, margins map[string]Margin) []TimingRow {
	var rows []TimingRow
	for _, name := range slices.Sorted(maps.Keys(margins)) {
		row := TimingRow{Statistic: name, Base: none, Candidate: none, Margin: none}
		b, bok := percentile(base, name)
		c, cok := percentile(candidate, name)
		if bok {
			m := margins[name]
			margin := b*m.Percent/100 + m.Ms
			row.Base = strconv.FormatInt(b, 10)
			row.Margin = "± " + strconv.FormatInt(margin, 10)
			row.Within = cok && c >= b-margin && c <= b+margin
		}
		if cok {
			row.Candidate = strconv.FormatInt(c, 10)
		}
		rows = append(rows, row)
	}
	return rows
}

// TimingRun is one run of the timing comparison: which build it ran, and
// its statistics.
type TimingRun struct {
	// Label says which build ran, such as its commit.
	Label      string
	Statistics Statistics
}

// WriteTimingReport writes the timing comparison of the candidate with the
// base, compared as rows, to w, as Markdown.
func WriteTimingReport(w io.Writer, base, candidate TimingRun, rows []TimingRow) error {
	var b strings.Builder
	b.WriteString("# Timing comparison\n\n")
	b.WriteString(`The candidate's timing statistics, each compared with the base's, run on the same runner at the same time (docs/testing.md, "Timing margins"). In milliseconds.` + "\n\n")
	fmt.Fprintf(&b, "- Base: %s\n- Candidate: %s\n\n", base.Label, candidate.Label)
	b.WriteString("| Statistic | Base | Candidate | Margin | |\n|---|---|---|---|---|\n")
	for _, r := range rows {
		verdict := "**beyond the margin**"
		if r.Within {
			verdict = "within"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.Statistic, r.Base, r.Candidate, r.Margin, verdict)
	}
	b.WriteString("\nSamples of each statistic:\n\n| Statistic | Base | Candidate |\n|---|---|---|\n")
	names := slices.Sorted(maps.Keys(base.Statistics.Timing))
	for name := range candidate.Statistics.Timing {
		if _, ok := base.Statistics.Timing[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", name, base.Statistics.Timing[name].N, candidate.Statistics.Timing[name].N)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// percentile returns the statistic named, such as "handoff lag p95", from
// timing, and whether timing has it.
func percentile(timing map[string]Distribution, name string) (int64, bool) {
	i := strings.LastIndex(name, " ")
	if i < 0 {
		return 0, false
	}
	d, ok := timing[name[:i]]
	if !ok {
		return 0, false
	}
	switch name[i+1:] {
	case "p50":
		return d.P50Ms, true
	case "p95":
		return d.P95Ms, true
	}
	return 0, false
}
