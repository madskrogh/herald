package retry_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/retry"
)

// fixedRandom returns a random source that always yields r.
func fixedRandom(r float64) func() float64 {
	return func() float64 { return r }
}

func newSchedule(t *testing.T, scale float64, random func() float64) retry.Schedule {
	t.Helper()
	s, err := retry.New(scale, random)
	if err != nil {
		t.Fatalf("New(%v, random): %v", scale, err)
	}
	return s
}

// next returns s.Next(attempts), failing the test if it reports no Attempts
// left.
func next(t *testing.T, s retry.Schedule, attempts int) time.Duration {
	t.Helper()
	d, ok := s.Next(attempts)
	if !ok {
		t.Fatalf("Next(%d) reports no Attempts left", attempts)
	}
	return d
}

func TestNextWithoutJitter(t *testing.T) {
	t.Parallel()

	// 0.5 is the midpoint of the jitter range, so delays come out unchanged.
	s := newSchedule(t, 1, fixedRandom(0.5))
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 0},
		{1, 5 * time.Second},
		{2, time.Minute},
		{3, 10 * time.Minute},
		{4, time.Hour},
		{5, 3 * time.Hour},
		{6, 6 * time.Hour},
		{7, 12 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("after %d Attempts", tt.attempts), func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, next(t, s, tt.attempts)); diff != "" {
				t.Errorf("Next(%d) mismatch (-want +got):\n%s", tt.attempts, diff)
			}
		})
	}
}

func TestNextJitterBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		scale    float64
		attempts int
		min, max time.Duration
	}{
		{1, 0, 0, 0},
		{1, 1, 4500 * time.Millisecond, 5500 * time.Millisecond},
		{1, 2, 54 * time.Second, 66 * time.Second},
		{1, 3, 9 * time.Minute, 11 * time.Minute},
		{1, 4, 54 * time.Minute, 66 * time.Minute},
		{1, 5, 2*time.Hour + 42*time.Minute, 3*time.Hour + 18*time.Minute},
		{1, 6, 5*time.Hour + 24*time.Minute, 6*time.Hour + 36*time.Minute},
		{1, 7, 10*time.Hour + 48*time.Minute, 13*time.Hour + 12*time.Minute},
		// Jitter scales with the delay: 43.2s ± 10%, not 12h ± 10%.
		{0.001, 1, 4500 * time.Microsecond, 5500 * time.Microsecond},
		{0.001, 7, 38880 * time.Millisecond, 47520 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("scale %v after %d Attempts", tt.scale, tt.attempts), func(t *testing.T) {
			t.Parallel()
			// The random source yields values in [0, 1), so these are the extremes.
			lowest := newSchedule(t, tt.scale, fixedRandom(0))
			highest := newSchedule(t, tt.scale, fixedRandom(math.Nextafter(1, 0)))
			if diff := cmp.Diff(tt.min, next(t, lowest, tt.attempts)); diff != "" {
				t.Errorf("Next(%d) with lowest jitter mismatch (-want +got):\n%s", tt.attempts, diff)
			}
			if diff := cmp.Diff(tt.max, next(t, highest, tt.attempts)); diff != "" {
				t.Errorf("Next(%d) with highest jitter mismatch (-want +got):\n%s", tt.attempts, diff)
			}
		})
	}
}

func TestNextScaled(t *testing.T) {
	t.Parallel()

	s := newSchedule(t, 0.001, fixedRandom(0.5))
	tests := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 0},
		{1, 5 * time.Millisecond},
		{4, 3600 * time.Millisecond},
		{7, 43200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("after %d Attempts", tt.attempts), func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, next(t, s, tt.attempts)); diff != "" {
				t.Errorf("Next(%d) at scale 0.001 mismatch (-want +got):\n%s", tt.attempts, diff)
			}
		})
	}
}

func TestNewRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		scale  float64
		random func() float64
	}{
		{"zero scale", 0, fixedRandom(0.5)},
		{"negative scale", -1, fixedRandom(0.5)},
		{"scale above 1", 2, fixedRandom(0.5)},
		{"infinite scale", math.Inf(1), fixedRandom(0.5)},
		{"NaN scale", math.NaN(), fixedRandom(0.5)},
		{"nil random", 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := retry.New(tt.scale, tt.random); err == nil {
				t.Errorf("New(%v, random) returned no error", tt.scale)
			}
		})
	}
}

func TestNextAfterLastAttempt(t *testing.T) {
	t.Parallel()

	s := newSchedule(t, 1, fixedRandom(0.5))
	for _, attempts := range []int{8, 9} {
		t.Run(fmt.Sprintf("after %d Attempts", attempts), func(t *testing.T) {
			t.Parallel()
			if got, ok := s.Next(attempts); ok {
				t.Errorf("Next(%d) = %v, want no Attempts left", attempts, got)
			}
		})
	}
}
