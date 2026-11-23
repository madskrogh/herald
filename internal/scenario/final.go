package scenario

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/ingest"
)

// The rules CheckFinalState checks, in the order it reports them.
const (
	ruleFinalState = "final state"
	ruleOutbox     = "outbox"
	ruleDuplicates = "duplicates"
)

// attemptTimeout is how long an Endpoint has to answer an Attempt with a
// 2xx for it to succeed (docs/design.md, Delivery semantics).
const attemptTimeout = 15 * time.Second

// FinalState is where a run left its Messages in Postgres.
type FinalState struct {
	// Messages are every Message of the run's Tenant.
	Messages []ingest.MessageStatus
	// Deliveries are every Delivery of those Messages.
	Deliveries []delivery.Delivery
}

// CheckFinalState checks the rules that hold for where a run left its
// Messages and Deliveries, once every phase has finished:
//
//   - every Delivery ended succeeded or failed, and succeeded exactly when
//     the last Attempt its Endpoint received got a 2xx within 15 s;
//   - every Message was handed off, and none was Parked: the scenario sends
//     none that a dispatcher rejects;
//   - a Message sent again with the same Idempotency-Key returned the first
//     one rather than creating another, and each Message has exactly one
//     Delivery per Endpoint whose filter matches it.
//
// Each rule also fails when the run gives it nothing to check.
func CheckFinalState(obs Observations, state FinalState) []Violation {
	obs.Deliveries = state.Deliveries
	e := newEdge(obs)
	add, nothing := e.add, e.nothingToCheck

	if len(state.Deliveries) == 0 {
		nothing(ruleFinalState, "Deliveries in Postgres")
	}
	for _, d := range state.Deliveries {
		k := deliveryKey{e.names[d.MessageID], obs.EndpointIDs[d.Endpoint.ID]}
		if d.State != delivery.StateSucceeded && d.State != delivery.StateFailed {
			add(ruleFinalState, k.String(), fmt.Sprintf("Delivery %s is %s, want succeeded or failed", d.ID, d.State))
			continue
		}
		attempts := e.delivery(k).attempts
		if len(attempts) == 0 {
			add(ruleFinalState, k.String(), fmt.Sprintf("Delivery %s is %s, but its Endpoint received no Attempt", d.ID, d.State))
			continue
		}
		last := attempts[len(attempts)-1]
		if succeeded := d.State == delivery.StateSucceeded; succeeded != answeredInTime(last) {
			add(ruleFinalState, k.String(), fmt.Sprintf("Delivery %s is %s, but its last Attempt was answered %s after %v", d.ID, d.State, last.Answer, last.AnsweredAt.Sub(last.ReceivedAt)))
		}
	}

	// sentBy maps each Message ID Herald returned to the names of the
	// Messages it was returned for.
	sentBy := map[uuid.UUID][]string{}
	for _, phase := range obs.Scenario.Phases {
		for _, m := range phase.Messages {
			if sent, ok := obs.Sent[m.Name]; ok && sent.Status == 0 {
				sentBy[sent.ID] = append(sentBy[sent.ID], m.Name)
			}
		}
	}
	if len(state.Messages) == 0 {
		nothing(ruleOutbox, "Messages in Postgres")
		nothing(ruleDuplicates, "Messages in Postgres")
	}
	for _, m := range state.Messages {
		subject := m.ID.String()
		if names := sentBy[m.ID]; len(names) > 0 {
			subject = names[0]
		}
		switch {
		case m.Parked:
			add(ruleOutbox, subject, fmt.Sprintf("Message %s is Parked, though the scenario sends none that a dispatcher rejects", m.ID))
		case !m.HandedOff:
			add(ruleOutbox, subject, fmt.Sprintf("Message %s is still in the Outbox", m.ID))
		}
		if len(sentBy[m.ID]) == 0 {
			add(ruleDuplicates, subject, "a Message the scenario didn't send")
		}
	}

	for _, r := range keyedRepeats(obs) {
		sent, origSent := obs.Sent[r.repeat.Name], obs.Sent[r.original.Name]
		if r.sameRequest() && sent.ID != origSent.ID {
			add(ruleDuplicates, r.repeat.Name, fmt.Sprintf("sent again with %s's Idempotency-Key, it created Message %s rather than returning %s", r.original.Name, sent.ID, origSent.ID))
		}
	}

	// count is how many Deliveries Herald stored for each Message and
	// Endpoint.
	count := map[deliveryKey]int{}
	for _, d := range state.Deliveries {
		count[deliveryKey{e.names[d.MessageID], obs.EndpointIDs[d.Endpoint.ID]}]++
	}
	want := e.matching()
	for _, k := range sortedKeys(want, count) {
		switch n := count[k]; {
		case want[k] && n != 1:
			add(ruleDuplicates, k.String(), fmt.Sprintf("%d Deliveries, want 1: the Endpoint's filter matches the Message", n))
		case !want[k]:
			add(ruleDuplicates, k.String(), fmt.Sprintf("%d Deliveries, want none: the Endpoint's filter doesn't match the Message", n))
		}
	}

	return sortViolations(e.violations, ruleFinalState, ruleOutbox, ruleDuplicates)
}

// answeredInTime reports whether an Endpoint answered a with a 2xx within
// the timeout.
func answeredInTime(a endpointtest.Attempt) bool {
	return succeeded(a) && !a.AnsweredAt.IsZero() && a.AnsweredAt.Sub(a.ReceivedAt) <= attemptTimeout
}

// sortedKeys returns the keys of a and b, in order of name.
func sortedKeys[V1, V2 any](a map[deliveryKey]V1, b map[deliveryKey]V2) []deliveryKey {
	var keys []deliveryKey
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(x, y deliveryKey) int { return strings.Compare(x.String(), y.String()) })
	return keys
}
