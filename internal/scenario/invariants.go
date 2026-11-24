package scenario

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/madskrogh/herald/internal/e2e"
)

// Violation is one way a run broke a scenario invariant: a rule that holds
// on any build, so a violation is a bug, not a change to approve
// (docs/testing.md).
type Violation struct {
	// Rule names the invariant, such as "signature" or "fan-out".
	Rule string
	// Subject names what broke it: a Delivery as "<message> to <endpoint>",
	// an Endpoint, a Message, a span or a service. It is empty when the run
	// has nothing for the rule to check.
	Subject string
	// Detail says how.
	Detail string
}

func (v Violation) String() string {
	if v.Subject == "" {
		return fmt.Sprintf("%s: %s", v.Rule, v.Detail)
	}
	return fmt.Sprintf("%s: %s: %s", v.Rule, v.Subject, v.Detail)
}

// services are the services whose goroutines the invariants check.
var services = []string{"herald-api", "herald-dispatcher"}

// warmUp is a scenario of one Message to one Endpoint. Delivering it starts
// what a service starts on first use and keeps, such as herald-api's
// subscription for NATS replies, so that those goroutines are in the idle
// set.
var warmUp = Scenario{
	Applications: []Application{{Name: "warm-up", Endpoints: []Endpoint{{Name: "warm-up/ep-1", Behaviour: Succeed}}}},
	Phases: []Phase{{Messages: []Message{
		{Name: "w001", Application: "warm-up", EventType: "warm.up", Payload: `{"message":"w001"}`},
	}}},
}

// IdleGoroutines delivers a Message, and then returns each service's
// goroutines, by service name. Take them before a run, while the stack is
// otherwise idle, for CheckInvariants to compare with.
func IdleGoroutines(ctx context.Context, stack *e2e.Stack, logger *slog.Logger) (map[string]Goroutines, error) {
	if _, err := Run(ctx, stack, Compose{}, warmUp, logger); err != nil {
		return nil, fmt.Errorf("warm up: %w", err)
	}
	idle := map[string]Goroutines{}
	for _, service := range services {
		g, err := goroutines(ctx, stack, service)
		if err != nil {
			return nil, err
		}
		idle[service] = g
	}
	return idle, nil
}

// keyedRepeat is a Message the scenario sent with an Idempotency-Key that
// an earlier Message, which Herald accepted, used: the original.
type keyedRepeat struct{ repeat, original Message }

// sameRequest reports whether the repeat is the original's request sent
// again, which returns the original Message, rather than another request,
// which the Tenant API refuses.
func (r keyedRepeat) sameRequest() bool { return r.repeat.Payload == r.original.Payload }

// keyedRepeats returns the Messages of obs's scenario sent with an
// Idempotency-Key that an earlier, accepted Message used, in the order
// sent.
func keyedRepeats(obs Observations) []keyedRepeat {
	originals := map[string]Message{}
	var rs []keyedRepeat
	for _, phase := range obs.Scenario.Phases {
		for _, m := range phase.Messages {
			if m.IdempotencyKey == "" {
				continue
			}
			if orig, ok := originals[m.IdempotencyKey]; ok {
				rs = append(rs, keyedRepeat{repeat: m, original: orig})
				continue
			}
			if sent, ok := obs.Sent[m.Name]; ok && sent.Status == 0 {
				originals[m.IdempotencyKey] = m
			}
		}
	}
	return rs
}

// sortViolations sorts vs by rule, in order, and then by subject, and
// returns it.
func sortViolations(vs []Violation, order ...string) []Violation {
	slices.SortStableFunc(vs, func(a, b Violation) int {
		return cmp.Or(
			cmp.Compare(slices.Index(order, a.Rule), slices.Index(order, b.Rule)),
			strings.Compare(a.Subject, b.Subject),
		)
	})
	return vs
}

func goroutines(ctx context.Context, stack *e2e.Stack, service string) (Goroutines, error) {
	profile, err := stack.GoroutineProfile(ctx, service)
	if err != nil {
		return nil, err
	}
	g, err := ParseGoroutines(profile)
	if err != nil {
		return nil, fmt.Errorf("goroutine profile of %s: %w", service, err)
	}
	return g, nil
}

// How SettledSpans and CheckInvariants wait for the stack to settle after a
// run. The
// Collector exports spans in batches, so the last arrive a moment after the
// run; and a service's goroutines end only once the connections they serve
// close.
const (
	settleTimeout  = 2 * time.Minute
	settleInterval = time.Second
)

// CheckInvariants checks every scenario invariant on a finished run of
// obs's scenario: at the edge, in the final state in Postgres, in its spans
// (SettledSpans), and in each service's goroutines compared with idle. It
// closes the stack's fake Endpoints, and the Stack's connections to the
// Tenant API, so that the services' goroutines serving them end.
//
// Goroutines are read again until they keep the rules or settleTimeout
// passes, and the violations of the last reading are returned. The error
// is for a stack that can't be read.
func CheckInvariants(ctx context.Context, stack *e2e.Stack, obs Observations, spans []e2e.Span, idle map[string]Goroutines) ([]Violation, error) {
	vs := CheckEdge(obs)

	var state FinalState
	messages, err := stack.TenantMessages(ctx, obs.TenantID)
	if err != nil {
		return nil, fmt.Errorf("read the final state: %w", err)
	}
	state.Messages = messages
	for _, m := range messages {
		deliveries, err := stack.MessageDeliveries(ctx, m.ID)
		if err != nil {
			return nil, fmt.Errorf("read the final state: %w", err)
		}
		state.Deliveries = append(state.Deliveries, deliveries...)
	}
	vs = append(vs, CheckFinalState(obs, state)...)

	vs = append(vs, CheckSpans(obs, spans)...)

	stack.Endpoints.Close()
	stack.CloseIdleConnections()
	for _, service := range services {
		var leaks []Violation
		err := settle(ctx, func() (bool, error) {
			after, err := goroutines(ctx, stack, service)
			if err != nil {
				return false, err
			}
			leaks = CompareGoroutines(service, idle[service], after)
			return len(leaks) == 0, nil
		})
		if err != nil {
			return nil, err
		}
		vs = append(vs, leaks...)
	}
	return vs, nil
}

// SettledSpans returns the spans of a finished run of obs's scenario, once
// they are complete: once every trace and parent is there (CheckSpans), or
// settleTimeout has passed. The statistics and the invariants read the
// spans there are then.
func SettledSpans(ctx context.Context, stack *e2e.Stack, obs Observations) ([]e2e.Span, error) {
	var spans []e2e.Span
	err := settle(ctx, func() (bool, error) {
		var err error
		spans, err = stack.RunSpans(ctx, obs.TenantID.String())
		if err != nil {
			return false, fmt.Errorf("read the run's spans: %w", err)
		}
		return !slices.ContainsFunc(CheckSpans(obs, spans), func(v Violation) bool {
			return v.Rule == ruleTrace || v.Rule == ruleParent
		}), nil
	})
	return spans, err
}

// settle calls check every settleInterval until it reports done, or
// settleTimeout passes, which isn't an error: the caller reports what check
// found last. It fails if check does, or ctx ends.
func settle(ctx context.Context, check func() (done bool, err error)) error {
	deadline := time.Now().Add(settleTimeout)
	tick := time.NewTicker(settleInterval)
	defer tick.Stop()
	for {
		done, err := check()
		if err != nil || done || time.Now().After(deadline) {
			return err
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}
