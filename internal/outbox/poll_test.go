package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// outcome is how a scripted round ends.
type outcome int

const (
	full  outcome = iota // handed off a full Batch; more may be waiting
	idle                 // handed off what was waiting, if anything
	fails                // failed
)

func TestPollPacesRounds(t *testing.T) {
	t.Parallel()

	const (
		interval   = time.Second
		maxBackoff = 4 * time.Second
	)
	tests := []struct {
		name   string
		rounds []outcome
		// When each round starts, after the first. The round after the
		// scripted ones stops the loop.
		want []time.Duration
	}{
		{
			name:   "full rounds follow each other at once",
			rounds: []outcome{full, full, idle},
			want:   []time.Duration{0, 0, 0, 1 * time.Second},
		},
		{
			name:   "idle rounds wait the interval",
			rounds: []outcome{idle, idle},
			want:   []time.Duration{0, 1 * time.Second, 2 * time.Second},
		},
		{
			// Waits of 1s, 2s, 4s, 4s (the cap), then 1s after the success
			// and 1s after the next failure: the backoff starts over.
			name:   "failures back off up to the cap, until a round succeeds",
			rounds: []outcome{fails, fails, fails, fails, idle, fails},
			want:   []time.Duration{0, 1 * time.Second, 3 * time.Second, 7 * time.Second, 11 * time.Second, 12 * time.Second, 13 * time.Second},
		},
		{
			name:   "a failed full round backs off too",
			rounds: []outcome{full, fails},
			want:   []time.Duration{0, 0, 1 * time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				start := time.Now()
				var got []time.Duration
				round := func(context.Context) (bool, error) {
					i := len(got)
					got = append(got, time.Since(start))
					if i == len(tt.rounds) {
						cancel()
						return false, nil
					}
					switch tt.rounds[i] {
					case full:
						return true, nil
					case fails:
						return true, errors.New("no dispatcher")
					default:
						return false, nil
					}
				}

				poll(ctx, round, interval, maxBackoff, slog.New(slog.DiscardHandler))

				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("round start times (-want +got):\n%s", diff)
				}
			})
		})
	}
}

func TestPollStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rounds := 0
		round := func(context.Context) (bool, error) {
			rounds++
			return false, nil
		}
		done := make(chan struct{})
		go func() {
			poll(ctx, round, time.Minute, time.Hour, slog.New(slog.DiscardHandler))
			close(done)
		}()

		// Cancelled while it waits for the next round, poll returns at once
		// rather than at the end of the wait.
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("poll still running after its context was cancelled")
		}
		if rounds != 1 {
			t.Errorf("ran %d rounds, want 1", rounds)
		}
	})
}
