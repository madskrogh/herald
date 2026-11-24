package scenario

import (
	"math"
	"time"
)

// retryWindow returns the shortest and longest delay after Attempt failed
// fails, before the next, with every delay and its jitter multiplied by
// scale (R-11, R-76). It is zero after no failed Attempt, or after the
// last one Herald makes.
//
// It is the harness's own retry schedule, built from the spec rather than
// imported from Herald, so that a bug in Herald's schedule can't become
// what the retry-schedule check expects (ADR 0007, "The harness").
func retryWindow(failed int, scale float64) (shortest, longest time.Duration) {
	if failed < 1 || failed > len(retryDelays) {
		return 0, 0
	}
	d := float64(retryDelays[failed-1]) * scale
	return time.Duration(math.Round(d * (1 - retryJitter))), time.Duration(math.Round(d * (1 + retryJitter)))
}
