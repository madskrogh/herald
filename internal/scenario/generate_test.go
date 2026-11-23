package scenario_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

func TestGenerateIsRepeatable(t *testing.T) {
	t.Parallel()
	if diff := cmp.Diff(scenario.Generate(7), scenario.Generate(7)); diff != "" {
		t.Errorf("Generate(7) twice (-first +second):\n%s", diff)
	}
	if cmp.Equal(scenario.Generate(7), scenario.Generate(8)) {
		t.Error("Generate(7) and Generate(8) are the same scenario")
	}
}

// TestGenerateCoversEverything checks, for many seeds, that the scenario has
// every Endpoint behaviour, fault and kind of Idempotency-Key the comparison
// depends on, and that every Endpoint gets a Delivery.
func TestGenerateCoversEverything(t *testing.T) {
	t.Parallel()
	// From the issue: every Endpoint behaviour and every fault.
	wantBehaviours := []scenario.Behaviour{
		scenario.Succeed, scenario.Fail, scenario.Slow, scenario.TimeOut,
		scenario.Reset, scenario.Malformed, scenario.Oversized,
		scenario.Redirect, scenario.TLSError, scenario.Flap,
	}
	wantFaults := []scenario.Fault{scenario.DispatcherRestarted, scenario.NATSPaused, scenario.PostgresSlowed}
	// Endpoints whose Deliveries take more than one Attempt, or whose
	// Attempts take seconds. With more Deliveries in a phase than the 4
	// Attempts an Endpoint may have in flight, one Delivery's Attempts would
	// wait for another's, and their timing would vary from run to run.
	bounded := map[scenario.Behaviour]bool{
		scenario.Fail: true, scenario.Reset: true, scenario.Malformed: true, scenario.Flap: true,
		scenario.Slow: true, scenario.TimeOut: true, scenario.Redirect: true, scenario.TLSError: true,
	}

	// sameEveryTime are the behaviours that answer every Attempt the same.
	// While Postgres is slowed, a dispatcher may make an Attempt again, and
	// any other behaviour would answer the later Attempts differently.
	sameEveryTime := map[scenario.Behaviour]bool{scenario.Succeed: true, scenario.Oversized: true}

	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			s := scenario.Generate(seed)
			if s.Seed != seed {
				t.Errorf("Seed = %d, want %d", s.Seed, seed)
			}

			deliveries := map[string]int{}
			endpoints := map[string]scenario.Endpoint{}
			behaviours := map[scenario.Behaviour]bool{}
			for _, app := range s.Applications {
				for _, ep := range app.Endpoints {
					endpoints[ep.Name] = ep
					behaviours[ep.Behaviour] = true
				}
			}
			faults := map[scenario.Fault]bool{}
			keys := map[string]scenario.Message{}
			var sent []scenario.Message
			var repeats, conflicts int
			for i, phase := range s.Phases {
				faults[phase.Fault] = true
				inPhase := map[string]int{}
				for _, m := range phase.Messages {
					if prev, ok := keys[m.IdempotencyKey]; ok && m.IdempotencyKey != "" {
						if prev.Application == m.Application && prev.EventType == m.EventType && prev.Payload == m.Payload {
							repeats++
							continue
						}
						conflicts++
						continue
					}
					keys[m.IdempotencyKey] = m
					sent = append(sent, m)
					reached := 0
					for _, ep := range application(t, s, m.Application).Endpoints {
						if matches(t, ep.Filter, m.EventType) {
							deliveries[ep.Name]++
							inPhase[ep.Name]++
							reached++
						}
					}
					if reached == 0 {
						t.Errorf("Message %s (%s) reaches no Endpoint", m.Name, m.EventType)
					}
				}
				for name, n := range inPhase {
					ep := endpoints[name]
					if bounded[ep.Behaviour] && n > 4 {
						t.Errorf("Endpoint %s (%s) gets %d Deliveries in phase %d, more than it may have in flight", name, ep.Behaviour, n, i+1)
					}
					if phase.Fault == scenario.PostgresSlowed && !sameEveryTime[ep.Behaviour] {
						t.Errorf("Endpoint %s (%s) gets a Delivery while Postgres is slowed", name, ep.Behaviour)
					}
				}
			}

			for _, b := range wantBehaviours {
				if !behaviours[b] {
					t.Errorf("no Endpoint behaves as %q", b)
				}
			}
			for _, f := range wantFaults {
				if !faults[f] {
					t.Errorf("no phase has fault %q", f)
				}
			}
			var small, large int
			for _, m := range sent {
				switch {
				case len(m.Payload) < 1<<10:
					small++
				case len(m.Payload) >= 64<<10:
					large++
				}
			}
			if small == 0 || large == 0 {
				t.Errorf("%d payloads under 1 KiB and %d of 64 KiB or more, want some of each", small, large)
			}
			if repeats == 0 || conflicts == 0 {
				t.Errorf("%d repeated and %d conflicting Idempotency-Keys, want some of each", repeats, conflicts)
			}
			for name, ep := range endpoints {
				n := deliveries[name]
				switch {
				case n == 0:
					t.Errorf("Endpoint %s (%s) gets no Delivery", name, ep.Behaviour)
				case ep.Behaviour == scenario.TLSError && n != 1:
					t.Errorf("Endpoint %s gets %d Deliveries; a TLS handshake carries no webhook-id, so it must get one", name, n)
				}
			}
		})
	}
}

func application(t *testing.T, s scenario.Scenario, name string) scenario.Application {
	t.Helper()
	for _, app := range s.Applications {
		if app.Name == name {
			return app
		}
	}
	t.Fatalf("no Application %q", name)
	return scenario.Application{}
}

// matches reports whether a Message of Event type et reaches an Endpoint
// with filter, by the harness's own matching, which follows the spec.
func matches(t *testing.T, filter []string, et string) bool {
	t.Helper()
	return scenario.Matches(filter, et)
}
