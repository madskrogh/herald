package scenario

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// Result is what a run of the scenario found: its behaviour comparison and
// its invariants, as far as it got. TestScenario writes it to result.json
// beside the run's statistics, for the report.
type Result struct {
	// Stopped says why the run stopped before it finished, if it did.
	Stopped string `json:"stopped,omitempty"`
	// Recorded says the run recorded new baselines (make scenario-record)
	// instead of comparing with them.
	Recorded bool `json:"recorded,omitempty"`
	// Behaviour is the behaviour comparison, or nil if the run didn't
	// compare.
	Behaviour *Differences `json:"behaviour,omitempty"`
	// Invariants are the scenario invariants, or nil if the run didn't
	// check them.
	Invariants *Invariants `json:"invariants,omitempty"`
}

// Differences are how a run differs from the committed baselines.
type Differences struct {
	// Edge is how what the Endpoints received differs from the edge
	// baseline (Compare), and Counts how the span counts differ from
	// theirs (CompareCounts).
	Edge   []Difference `json:"edge"`
	Counts []Difference `json:"counts"`
}

// Invariants is how a run broke the scenario invariants: every invariant
// with no Violation held.
type Invariants struct {
	Violations []Violation `json:"violations"`
}

// invariantRules are every scenario invariant, in the order
// CheckInvariants checks them.
var invariantRules = slices.Concat(edgeRules, finalStateRules, spanRules, []string{ruleGoroutines})

// Report is everything a run of the scenario found, for WriteReport.
type Report struct {
	// Candidate is the run of the build under test.
	Candidate Result
	// Timing is the timing comparison with the base build's run, or nil
	// for a run without one (make scenario-compare).
	Timing *TimingReport
}

// TimingReport is the timing comparison of the candidate's run with the
// base's, run on the same machine at the same time.
type TimingReport struct {
	Base, Candidate TimingRun
	Rows            []TimingRow
	// Error says why the comparison couldn't run, if it couldn't: then
	// Rows is empty.
	Error string
}

// TimingRun is one run of the timing comparison: which build it ran, and
// its statistics.
type TimingRun struct {
	// Label says which build ran, such as its commit.
	Label      string
	Statistics Statistics
}

// WriteReport writes r to w as Markdown: a summary, then the behaviour
// comparison, the invariants, and the timing comparison if r has one.
func WriteReport(w io.Writer, r Report) error {
	var b strings.Builder
	b.WriteString("# Scenario report\n\n")
	c := r.Candidate
	if c.Stopped != "" {
		b.WriteString("The candidate's run stopped before it finished, so what follows is what it found until then:\n\n")
		writeBlock(&b, []string{c.Stopped})
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- Behaviour comparison: %s\n", behaviourSummary(c))
	fmt.Fprintf(&b, "- Scenario invariants: %s\n", invariantsSummary(c.Invariants))
	if r.Timing != nil {
		fmt.Fprintf(&b, "- Timing comparison: %s\n", r.Timing.summary())
	}

	b.WriteString("\n## Behaviour comparison\n\n")
	b.WriteString(`What the Endpoints received, and the span counts, compared with the committed baselines (docs/testing.md, "Behaviour comparison"). Each difference is as make scenario-compare reports it.` + "\n\n")
	switch {
	case c.Recorded:
		b.WriteString("Not compared: this run recorded new baselines.\n")
	case c.Behaviour == nil:
		b.WriteString("Not compared: the run stopped first.\n")
	default:
		writeDifferences(&b, "What the Endpoints received", "testdata/baseline.json", c.Behaviour.Edge)
		b.WriteString("\n")
		writeDifferences(&b, "Span counts", "testdata/counts.json", c.Behaviour.Counts)
	}

	b.WriteString("\n## Scenario invariants\n\n")
	b.WriteString(`The rules that hold on any build, checked after the run (docs/testing.md, "Scenario invariants").` + "\n\n")
	if c.Invariants == nil {
		b.WriteString("Not checked: the run stopped first.\n")
	} else {
		writeInvariants(&b, c.Invariants.Violations)
	}

	if r.Timing != nil {
		b.WriteString("\n")
		r.Timing.write(&b)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func behaviourSummary(c Result) string {
	switch {
	case c.Recorded:
		return "not compared: new baselines recorded"
	case c.Behaviour == nil:
		return "not compared"
	}
	return count(len(c.Behaviour.Edge)+len(c.Behaviour.Counts), "no differences", "**%d difference**", "**%d differences**")
}

func invariantsSummary(inv *Invariants) string {
	if inv == nil {
		return "not checked"
	}
	broken := len(brokenRules(inv.Violations))
	if broken == 0 {
		return fmt.Sprintf("all %d held", len(invariantRules))
	}
	return fmt.Sprintf("**%d of %d broken**", broken, len(invariantRules))
}

// count formats n with zero, one or many, the last two holding a %d.
func count(n int, zero, one, many string) string {
	switch n {
	case 0:
		return zero
	case 1:
		return fmt.Sprintf(one, n)
	}
	return fmt.Sprintf(many, n)
}

func writeDifferences(b *strings.Builder, what, baseline string, diffs []Difference) {
	fmt.Fprintf(b, "%s, against %s: ", what, baseline)
	if len(diffs) == 0 {
		b.WriteString("no differences.\n")
		return
	}
	b.WriteString(count(len(diffs), "", "%d difference.", "%d differences.") + "\n\n")
	lines := make([]string, len(diffs))
	for i, d := range diffs {
		lines[i] = d.String()
	}
	writeBlock(b, lines)
}

// writeInvariants writes whether each invariant held, then each Violation,
// by rule in the order they are checked.
func writeInvariants(b *strings.Builder, vs []Violation) {
	broken := brokenRules(vs)
	b.WriteString("| Invariant | |\n|---|---|\n")
	for _, rule := range invariantRules {
		verdict := "held"
		if n := broken[rule]; n > 0 {
			verdict = fmt.Sprintf("**broken**: %s", count(n, "", "%d violation", "%d violations"))
		}
		fmt.Fprintf(b, "| %s | %s |\n", rule, verdict)
	}
	if len(vs) == 0 {
		return
	}
	vs = slices.Clone(vs)
	slices.SortStableFunc(vs, func(a, b Violation) int {
		return ruleIndex(a.Rule) - ruleIndex(b.Rule)
	})
	b.WriteString("\nEach violation:\n\n")
	lines := make([]string, len(vs))
	for i, v := range vs {
		lines[i] = v.String()
	}
	writeBlock(b, lines)
}

// brokenRules counts the Violations of each rule.
func brokenRules(vs []Violation) map[string]int {
	broken := map[string]int{}
	for _, v := range vs {
		broken[v.Rule]++
	}
	return broken
}

// ruleIndex is where rule is in invariantRules, and after them all for a
// rule that isn't one, so that its Violations are still reported.
func ruleIndex(rule string) int {
	if i := slices.Index(invariantRules, rule); i >= 0 {
		return i
	}
	return len(invariantRules)
}

// writeBlock writes lines as a fenced block, so that what they quote, such
// as a body or a header, isn't read as Markdown.
func writeBlock(b *strings.Builder, lines []string) {
	b.WriteString("```\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("```\n")
}

func (t *TimingReport) summary() string {
	if t.Error != "" {
		return "**couldn't compare**"
	}
	failed := 0
	for _, r := range t.Rows {
		if !r.Within {
			failed++
		}
	}
	return count(failed, "every statistic within its margin", "**%d statistic not within its margin**", "**%d statistics not within their margins**")
}

// write writes the timing comparison of the candidate with the base.
func (t *TimingReport) write(b *strings.Builder) {
	b.WriteString("## Timing comparison\n\n")
	b.WriteString(`The candidate's timing statistics, each compared with the base's, run on the same runner at the same time (docs/testing.md, "Timing margins"). In milliseconds.` + "\n\n")
	fmt.Fprintf(b, "- Base: %s\n- Candidate: %s\n\n", t.Base.Label, t.Candidate.Label)
	if t.Error != "" {
		b.WriteString("The comparison couldn't run, so the statistics weren't compared:\n\n")
		writeBlock(b, []string{t.Error})
		return
	}
	b.WriteString("| Statistic | Base | Candidate | Margin | |\n|---|---|---|---|---|\n")
	for _, r := range t.Rows {
		verdict := r.verdict()
		if !r.Within {
			verdict = "**" + verdict + "**"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", r.Key, formatMs(r.Base), formatMs(r.Candidate), r.margin(), verdict)
	}
	b.WriteString("\nSamples of each statistic:\n\n| Statistic | Base | Candidate |\n|---|---|---|\n")
	for _, name := range unionKeys(t.Base.Statistics.Timing, t.Candidate.Statistics.Timing) {
		fmt.Fprintf(b, "| %s | %d | %d |\n", name, t.Base.Statistics.Timing[name].N, t.Candidate.Statistics.Timing[name].N)
	}
}
