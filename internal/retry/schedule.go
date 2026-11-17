// Package retry provides the retry schedule for Deliveries: when each Attempt
// is due after the ones before it have failed.
package retry

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// delays holds the delay before each Attempt, indexed by the number of
// Attempts made before it.
var delays = []time.Duration{
	0,
	5 * time.Second,
	time.Minute,
	10 * time.Minute,
	time.Hour,
	3 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
}

// maxJitter is the fraction by which every delay but the first may vary either
// way, so Deliveries that failed together don't all retry at the same moment.
const maxJitter = 0.1

// Schedule is the retry schedule for Deliveries. Build one with New; the zero
// value is not usable.
type Schedule struct {
	scale  float64
	random func() float64
}

// New returns the retry schedule with every delay multiplied by scale, which
// is 1 in production and lets tests run the whole schedule in seconds. Scale
// only ever shrinks the schedule, which also keeps every delay within
// time.Duration's range.
//
// random returns values in [0, 1) and drives the jitter. Delivery workers
// share one Schedule, so random must be safe for concurrent use, as
// math/rand/v2's Float64 is (a *rand.Rand is not).
func New(scale float64, random func() float64) (Schedule, error) {
	// Written so that NaN fails too.
	if !(scale > 0 && scale <= 1) {
		return Schedule{}, fmt.Errorf("retry schedule scale must be in (0, 1], got %v", scale)
	}
	if random == nil {
		return Schedule{}, errors.New("retry schedule needs a random source")
	}
	return Schedule{scale: scale, random: random}, nil
}

// Next returns the delay before the next Attempt, given the number of
// Attempts made so far, or false when none are left. attempts must not be
// negative.
func (s Schedule) Next(attempts int) (time.Duration, bool) {
	if attempts >= len(delays) {
		return 0, false
	}
	// Maps random's [0, 1) onto [1-maxJitter, 1+maxJitter), with 0.5 giving
	// exactly 1.
	factor := s.scale * (1 + maxJitter*(2*s.random()-1))
	return time.Duration(math.Round(float64(delays[attempts]) * factor)), true
}
