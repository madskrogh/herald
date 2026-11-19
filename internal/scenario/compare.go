package scenario

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// Timing margins. An Attempt's time since its Delivery's first may differ
// from the baseline's by jitterMargin of it, plus slackMs. The retry
// schedule's jitter moves each delay by up to 10% either way, in the
// baseline's run as well as this one, so the two can be 20% of the schedule
// apart, which is up to 22% of the baseline's time. slackMs covers the
// delivery workers' polling and the machine. With the local stack's retry
// scale, a change to a retry delay of an hour or more fails the comparison,
// and shorter ones are lost in the margins (docs/design.md, Testing).
const (
	jitterMargin = 25 // percent
	slackMs      = 1000
)

// none stands for a value one side doesn't have.
const none = "(none)"

// Difference is one way a Recording differs from the baseline.
type Difference struct {
	// Delivery names the Delivery that differs, as "<message> to
	// <endpoint>", or the Message, or the fault, or nothing for the run as a
	// whole.
	Delivery string
	// Field names what differs, such as "state" or "attempt 2: body".
	Field     string
	Want, Got string
}

func (d Difference) String() string {
	where := d.Field
	if d.Delivery != "" {
		where = d.Delivery + ": " + d.Field
	}
	return fmt.Sprintf("%s: want %s, got %s", where, d.Want, d.Got)
}

// Compare returns how got differs from the baseline want: the run, then
// Message by Message, then Delivery by Delivery in want's order, and last
// the Deliveries only got has.
func Compare(want, got Recording) []Difference {
	var diffs []Difference
	add := func(where, field, w, g string) {
		if w != g {
			diffs = append(diffs, Difference{Delivery: where, Field: field, Want: w, Got: g})
		}
	}
	add("", "seed", strconv.FormatUint(want.Seed, 10), strconv.FormatUint(got.Seed, 10))
	add("", "faults", strconv.Itoa(len(want.Faults)), strconv.Itoa(len(got.Faults)))
	for i := range min(len(want.Faults), len(got.Faults)) {
		w, g := want.Faults[i], got.Faults[i]
		add("", fmt.Sprintf("fault %d", i+1), string(w.Fault), string(g.Fault))
		add(string(w.Fault), "late", strconv.FormatBool(w.Late), strconv.FormatBool(g.Late))
	}

	gotOutcomes := map[string]string{}
	for _, m := range got.Messages {
		gotOutcomes[m.Name] = m.Outcome
	}
	wantOutcomes := map[string]string{}
	for _, m := range want.Messages {
		wantOutcomes[m.Name] = m.Outcome
		add(m.Name, "outcome", m.Outcome, orNone(gotOutcomes, m.Name))
	}
	for _, m := range got.Messages {
		if _, ok := wantOutcomes[m.Name]; !ok {
			add(m.Name, "outcome", none, m.Outcome)
		}
	}

	gotDeliveries := map[string]DeliveryRecord{}
	for _, d := range got.Deliveries {
		gotDeliveries[d.name()] = d
	}
	wantDeliveries := map[string]bool{}
	for _, w := range want.Deliveries {
		name := w.name()
		wantDeliveries[name] = true
		g, ok := gotDeliveries[name]
		if !ok {
			add(name, "delivery", "present", none)
			continue
		}
		add(name, "behaviour", string(w.Behaviour), string(g.Behaviour))
		add(name, "fault", string(w.Fault), string(g.Fault))
		add(name, "state", string(w.State), string(g.State))
		add(name, "attempts", strconv.Itoa(len(w.Attempts)), strconv.Itoa(len(g.Attempts)))
		for i := range min(len(w.Attempts), len(g.Attempts)) {
			wa, ga := w.Attempts[i], g.Attempts[i]
			field := func(f string) string { return fmt.Sprintf("attempt %d: %s", i+1, f) }
			if !withinMargin(wa.AfterMs, ga.AfterMs) {
				add(name, field("after_ms"), formatMs(wa.AfterMs), formatMs(ga.AfterMs))
			}
			add(name, field("handshake"), strconv.FormatBool(wa.Handshake), strconv.FormatBool(ga.Handshake))
			headers := slices.Sorted(maps.Keys(wa.Header))
			for h := range ga.Header {
				if _, ok := wa.Header[h]; !ok {
					headers = append(headers, h)
				}
			}
			slices.Sort(headers)
			for _, h := range headers {
				add(name, field("header "+h), orNone(wa.Header, h), orNone(ga.Header, h))
			}
			add(name, field("body"), wa.Body, ga.Body)
			add(name, field("signature"), wa.Signature, ga.Signature)
			add(name, field("answer"), wa.Answer, ga.Answer)
		}
	}
	for _, g := range got.Deliveries {
		if name := g.name(); !wantDeliveries[name] {
			add(name, "delivery", none, "present")
		}
	}
	return diffs
}

// name names the Delivery in a Difference.
func (d DeliveryRecord) name() string {
	return d.Message + " to " + d.Endpoint
}

// withinMargin reports whether got is close enough to want, the baseline's
// time since the Delivery's first Attempt. Neither having one, under a
// fault, is close enough; only one having one is not.
func withinMargin(want, got *int64) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	margin := *want*jitterMargin/100 + slackMs
	return *got >= *want-margin && *got <= *want+margin
}

func formatMs(ms *int64) string {
	if ms == nil {
		return none
	}
	return strconv.FormatInt(*ms, 10)
}

// orNone returns m[k], or none if m doesn't have k.
func orNone(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return none
}
