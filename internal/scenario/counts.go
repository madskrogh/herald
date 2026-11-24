package scenario

import (
	"strconv"
)

// Counts are a run's span counts (Statistics.Counts) and the traffic file
// it ran: what the counts baseline holds. Like the edge recording, it is
// recorded, never written by hand.
type Counts struct {
	Traffic TrafficFile    `json:"traffic"`
	Counts  map[string]int `json:"counts"`
}

// CompareCounts returns how got differs from the baseline want: the traffic
// file first, then each count, by kind.
func CompareCounts(want, got Counts) []Difference {
	var diffs []Difference
	add := func(field, w, g string) {
		if w != g {
			diffs = append(diffs, Difference{Field: field, Want: w, Got: g})
		}
	}
	add("traffic file", want.Traffic.File, got.Traffic.File)
	add("traffic sha256", want.Traffic.SHA256, got.Traffic.SHA256)
	count := func(m map[string]int, k string) string {
		if n, ok := m[k]; ok {
			return strconv.Itoa(n)
		}
		return none
	}
	for _, k := range unionKeys(want.Counts, got.Counts) {
		add("spans: "+k, count(want.Counts, k), count(got.Counts, k))
	}
	return diffs
}
