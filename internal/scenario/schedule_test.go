package scenario

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// vectorDuration reads a duration as a vector writes it, such as "4.5 s",
// "2 h 42 m" or "38.88 s".
func vectorDuration(t *testing.T, cell string) time.Duration {
	t.Helper()
	units := map[string]time.Duration{"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour}
	f := strings.Fields(cell)
	if len(f) == 0 || len(f)%2 != 0 {
		t.Fatalf("can't read duration %q", cell)
	}
	var d time.Duration
	for i := 0; i < len(f); i += 2 {
		n, err := strconv.ParseFloat(f[i], 64)
		unit, ok := units[f[i+1]]
		if err != nil || !ok {
			t.Fatalf("can't read duration %q", cell)
		}
		d += time.Duration(n * float64(unit))
	}
	return d
}

func TestRetryWindowFollowsTheSpec(t *testing.T) {
	t.Parallel()

	type tc struct {
		name              string
		scale             float64
		failed            int
		shortest, longest time.Duration
	}
	var cases []tc
	for _, row := range vectors(t, "R-11") {
		cases = append(cases, tc{
			name: "R-11 after Attempt " + row["Failed Attempt"], scale: 1, failed: atoi(t, row["Failed Attempt"]),
			shortest: vectorDuration(t, row["Shortest"]), longest: vectorDuration(t, row["Longest"]),
		})
	}
	for _, row := range vectors(t, "R-76") {
		if row["Failed Attempt"] == "n/a" {
			continue // a scale the dispatcher refuses, which the harness never runs with
		}
		scale := 1.0
		if s := codeSpans(row["`HERALD_RETRY_SCALE`"]); len(s) == 1 {
			f, err := strconv.ParseFloat(s[0], 64)
			if err != nil {
				t.Fatal(err)
			}
			scale = f
		}
		cases = append(cases, tc{
			name: "R-76 scale " + strconv.FormatFloat(scale, 'g', -1, 64) + " after Attempt " + row["Failed Attempt"], scale: scale,
			failed:   atoi(t, row["Failed Attempt"]),
			shortest: vectorDuration(t, row["Shortest delay"]), longest: vectorDuration(t, row["Longest delay"]),
		})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			shortest, longest := retryWindow(c.failed, c.scale)
			if shortest != c.shortest || longest != c.longest {
				t.Errorf("retryWindow(%d, %v) = %v, %v, want %v, %v", c.failed, c.scale, shortest, longest, c.shortest, c.longest)
			}
		})
	}
}
