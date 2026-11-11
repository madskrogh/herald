package retry_test

import (
	"fmt"
	"math"
	"testing"
	"time"

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
			if got := next(t, s, tt.attempts); got != tt.want {
				t.Errorf("Next(%d) = %v, want %v", tt.attempts, got, tt.want)
			}
		})
	}
}

func TestNextJitterBounds(t *testing.T) {
	t.Parallel()

	// The random source yields values in [0, 1), so these are the extremes.
	lowest := newSchedule(t, 1, fixedRandom(0))
	highest := newSchedule(t, 1, fixedRandom(math.Nextafter(1, 0)))
	tests := []struct {
		attempts int
		min, max time.Duration
	}{
		{0, 0, 0},
		{1, 4500 * time.Millisecond, 5500 * time.Millisecond},
		{2, 54 * time.Second, 66 * time.Second},
		{3, 9 * time.Minute, 11 * time.Minute},
		{4, 54 * time.Minute, 66 * time.Minute},
		{5, 2*time.Hour + 42*time.Minute, 3*time.Hour + 18*time.Minute},
		{6, 5*time.Hour + 24*time.Minute, 6*time.Hour + 36*time.Minute},
		{7, 10*time.Hour + 48*time.Minute, 13*time.Hour + 12*time.Minute},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("after %d Attempts", tt.attempts), func(t *testing.T) {
			t.Parallel()
			if got := next(t, lowest, tt.attempts); got != tt.min {
				t.Errorf("Next(%d) with lowest jitter = %v, want %v", tt.attempts, got, tt.min)
			}
			if got := next(t, highest, tt.attempts); got != tt.max {
				t.Errorf("Next(%d) with highest jitter = %v, want %v", tt.attempts, got, tt.max)
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
			if got := next(t, s, tt.attempts); got != tt.want {
				t.Errorf("Next(%d) at scale 0.001 = %v, want %v", tt.attempts, got, tt.want)
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
