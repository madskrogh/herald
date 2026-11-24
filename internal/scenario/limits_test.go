package scenario

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/spec"
)

// TestLimitsFollowTheSpec compares each limit the harness holds Herald to,
// and each fault's size, with its value in spec/limits.md, so the checks
// can't drift from the spec.
func TestLimitsFollowTheSpec(t *testing.T) {
	t.Parallel()

	tables, err := spec.FileTables(specFS, "limits.md")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, tb := range tables {
		name, value := tb.Column("Limit"), tb.Column("Value")
		if name < 0 || value < 0 {
			continue
		}
		for i, r := range tb.Rows {
			if len(r) != len(tb.Header) {
				t.Fatalf("limits.md:%d: row %d has %d cells, want %d", tb.Line, i+1, len(r), len(tb.Header))
			}
			values[strings.Trim(r[name], "`")] = r[value]
		}
	}
	durations := func(ds ...time.Duration) string {
		parts := make([]string, len(ds))
		for i, d := range ds {
			parts[i] = specDuration(d)
		}
		return strings.Join(parts, ", ")
	}
	got := map[string]string{
		"attempt-timeout":            durations(attemptTimeout),
		"max-attempts":               strconv.Itoa(maxAttempts),
		"retry-delays":               durations(retryDelays...),
		"retry-jitter":               strconv.Itoa(int(retryJitter*100)) + "% either way",
		"max-in-flight-per-endpoint": strconv.Itoa(maxInFlight),
		"max-delay":                  durations(maxDelay),
		"max-delay-in-fault":         durations(maxDelayInFault),
		"event-type-bytes":           strconv.Itoa(eventTypeBytes),
		"filter-pattern-bytes":       strconv.Itoa(filterPatternBytes),
		"max-filter-patterns":        strconv.Itoa(maxFilterPatterns),
		"fault-nats-unavailable":     durations(natsPause),
		"fault-postgres-cpu":         strconv.FormatFloat(postgresCPU*100, 'g', -1, 64) + "%",
	}
	want := map[string]string{}
	for name := range got {
		v, ok := values[name]
		if !ok {
			t.Errorf("spec/limits.md has no limit %s", name)
		}
		want[name] = v
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the harness's limits differ from spec/limits.md (-spec +harness):\n%s", diff)
	}
}

// specDuration writes a whole duration as the spec does, such as "15 s",
// "1 m" or "12 h".
func specDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + " h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + " m"
	default:
		return strconv.Itoa(int(d/time.Second)) + " s"
	}
}
