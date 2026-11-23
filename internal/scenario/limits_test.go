package scenario

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/spec"
)

// TestLimitsFollowTheSpec compares each limit the harness holds Herald to
// with its value in spec/limits.md, so the checks can't drift from the
// spec.
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
		for _, r := range tb.Rows {
			values[strings.Trim(r[name], "`")] = r[value]
		}
	}
	join := func(ds []time.Duration) string {
		parts := make([]string, len(ds))
		for i, d := range ds {
			parts[i] = vectorDurationString(d)
		}
		return strings.Join(parts, ", ")
	}
	got := map[string]string{
		"attempt-timeout":            vectorDurationString(attemptTimeout),
		"max-attempts":               itoa(maxAttempts),
		"retry-delays":               join(retryDelays),
		"retry-jitter":               itoa(int(retryJitter*100)) + "% either way",
		"max-in-flight-per-endpoint": itoa(maxInFlight),
		"max-delay":                  vectorDurationString(maxDelay),
		"max-delay-in-fault":         vectorDurationString(maxDelayInFault),
		"event-type-bytes":           itoa(eventTypeBytes),
		"filter-pattern-bytes":       itoa(filterPatternBytes),
		"max-filter-patterns":        itoa(maxFilterPatterns),
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

// vectorDurationString writes a whole duration as the spec does, such as
// "15 s", "1 m" or "12 h".
func vectorDurationString(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return itoa(int(d/time.Hour)) + " h"
	case d%time.Minute == 0:
		return itoa(int(d/time.Minute)) + " m"
	default:
		return itoa(int(d/time.Second)) + " s"
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
