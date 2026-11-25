package scenario

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// Margin is how far a value may be from what it is compared with, either
// way: Percent of that value, plus Ms.
type Margin struct {
	Percent int64
	Ms      int64
}

// Of returns how far a value may be from want, in milliseconds. The
// percentage is of want's size: a value such as handoff lag can be
// negative.
func (m Margin) Of(want int64) int64 {
	return max(want, -want)*m.Percent/100 + m.Ms
}

// Allows reports whether got is within the margin of want.
func (m Margin) Allows(want, got int64) bool {
	margin := m.Of(want)
	return got >= want-margin && got <= want+margin
}

// Percentile is a percentile of a timing statistic's samples.
type Percentile int

// The percentiles Distribution holds.
const (
	P50 Percentile = 50
	P95 Percentile = 95
)

// TimingKey names what the timing comparison compares: a timing statistic
// of Statistics, and a percentile of it.
type TimingKey struct {
	Statistic  string
	Percentile Percentile
}

// String names k as the report and docs/testing.md do, such as "handoff
// lag p95".
func (k TimingKey) String() string {
	return fmt.Sprintf("%s p%d", k.Statistic, k.Percentile)
}

// at returns d's value at percentile p, and whether d holds it.
func (d Distribution) at(p Percentile) (int64, bool) {
	switch p {
	case P50:
		return d.P50Ms, true
	case P95:
		return d.P95Ms, true
	}
	return 0, false
}

// TimingRow is one compared value of both runs, in milliseconds.
type TimingRow struct {
	Key TimingKey
	// Base and Candidate are its value in each run, or nil for a run with
	// no samples for it.
	Base, Candidate *int64
	// Margin is how far apart they may be, from the base's value; 0 when
	// the base has none.
	Margin int64
	// Within says the candidate's is within the margin of the base's.
	Within bool
}

func (r TimingRow) String() string {
	return fmt.Sprintf("%s: base %s, candidate %s, margin %s (ms): %s",
		r.Key, formatMs(r.Base), formatMs(r.Candidate), r.margin(), r.verdict())
}

// margin writes how far apart the two runs' values may be.
func (r TimingRow) margin() string {
	if r.Base == nil {
		return none
	}
	return "± " + strconv.FormatInt(r.Margin, 10)
}

// verdict says how the comparison of the two runs' values came out. A
// value a run has no samples for fails it, as the invariants fail on
// missing data, but under its own name.
func (r TimingRow) verdict() string {
	switch {
	case r.Base == nil || r.Candidate == nil:
		return "no samples to compare"
	case r.Within:
		return "within"
	}
	return "beyond the margin"
}

// CompareTiming compares each value that margins names, of the candidate
// with the base's, run on the same machine at the same time, and returns
// them in order of name.
func CompareTiming(base, candidate map[string]Distribution, margins map[TimingKey]Margin) []TimingRow {
	keys := slices.SortedFunc(maps.Keys(margins), func(a, b TimingKey) int {
		return cmp.Compare(a.String(), b.String())
	})
	rows := make([]TimingRow, 0, len(keys))
	for _, k := range keys {
		row := TimingRow{Key: k}
		if v, ok := base[k.Statistic].at(k.Percentile); ok && base[k.Statistic].N > 0 {
			row.Base = &v
			row.Margin = margins[k].Of(v)
		}
		if v, ok := candidate[k.Statistic].at(k.Percentile); ok && candidate[k.Statistic].N > 0 {
			row.Candidate = &v
		}
		row.Within = row.Base != nil && row.Candidate != nil && margins[k].Allows(*row.Base, *row.Candidate)
		rows = append(rows, row)
	}
	return rows
}

// unionKeys returns the keys of a and b, sorted, each once.
func unionKeys[V any](a, b map[string]V) []string {
	keys := slices.Collect(maps.Keys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
