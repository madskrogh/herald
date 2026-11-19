package scenario

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// eventTypes are the Event types Messages are sent with.
var eventTypes = []string{
	"invoice.created", "invoice.paid", "invoice.voided",
	"customer.created", "customer.updated", "customer.deleted",
	"payout.sent", "payout.failed",
}

// fastBehaviours answer within milliseconds, so an Endpoint with any of them
// can take many Deliveries without one waiting for another.
var fastBehaviours = []Behaviour{Succeed, Fail, Reset, Malformed, Oversized, Flap}

// slowEndpoints are the behaviours whose Attempts take seconds, each given an
// Application of its own with one Endpoint, and how many Messages it gets at
// most: no more than the 4 Attempts an Endpoint may have in flight, so no
// Delivery waits for another, and one for TLSError, whose handshakes carry no
// webhook-id to tell Deliveries apart.
var slowEndpoints = []struct {
	behaviour Behaviour
	messages  int
}{
	{Slow, 3},
	{TimeOut, 2},
	{Redirect, 2},
	{TLSError, 1},
}

// Payload sizes, in bytes: most are small, some medium and a few large, well
// within the 256 KiB limit.
const (
	smallPayload  = 600
	mediumPayload = 16 << 10
	largePayload  = 200 << 10
)

// Generate returns the scenario generated from seed. The same seed always
// gives the same scenario.
func Generate(seed uint64) Scenario {
	g := &generator{r: rand.New(rand.NewPCG(seed, 0))}
	s := Scenario{Seed: seed}

	// Applications whose Endpoints answer fast. Each has every fast behaviour
	// between them, and the first Endpoint of each accepts every Event type,
	// so every Message has a Delivery.
	behaviours := append([]Behaviour(nil), fastBehaviours...)
	for range 2 + g.r.IntN(4) {
		behaviours = append(behaviours, pick(g.r, []Behaviour{Succeed, Succeed, Fail, Flap}))
	}
	g.r.Shuffle(len(behaviours), func(i, j int) { behaviours[i], behaviours[j] = behaviours[j], behaviours[i] })
	regular := make([]Application, 3+g.r.IntN(3))
	for i := range regular {
		regular[i].Name = fmt.Sprintf("app-%d", i+1)
	}
	var steady []*draft
	for i, b := range behaviours {
		app := &regular[i%len(regular)]
		ep := Endpoint{Name: fmt.Sprintf("%s/ep-%d", app.Name, len(app.Endpoints)+1), Behaviour: b}
		if len(app.Endpoints) > 0 {
			// A Message the filter accepts, so the Endpoint gets a Delivery.
			var anchor string
			ep.Filter, anchor = g.filter()
			steady = append(steady, g.draft(app.Name, anchor, g.size()))
		}
		app.Endpoints = append(app.Endpoints, ep)
	}
	s.Applications = append(s.Applications, regular...)

	for range 40 + g.r.IntN(21) {
		steady = append(steady, g.draft(pick(g.r, regular).Name, pick(g.r, eventTypes), g.size()))
	}
	g.r.Shuffle(len(steady), func(i, j int) { steady[i], steady[j] = steady[j], steady[i] })
	// At least one of each extreme, whatever the draws.
	steady[0].size, steady[1].size = largePayload, smallPayload
	var keyed []*draft
	for _, d := range steady {
		if g.r.IntN(5) < 2 {
			d.keyed = true
			keyed = append(keyed, d)
		}
	}
	// Sent again with the same key: some the same request, which Herald
	// answers with the original Message, and some a different one, which it
	// refuses.
	for i := range 2 + g.r.IntN(3) {
		orig := keyed[g.r.IntN(len(keyed))]
		steady = append(steady, &draft{Message: orig.Message, sameAs: orig, conflicts: i == 0})
	}

	for _, se := range slowEndpoints {
		app := g.application(&s, se.behaviour)
		for range 1 + g.r.IntN(se.messages) {
			steady = append(steady, g.draft(app.Name, pick(g.r, eventTypes), smallPayload))
		}
	}
	held := g.application(&s, TimeOut)

	phases := [][]*draft{
		steady,
		g.drafts(regular, 3+g.r.IntN(3)),
		g.drafts(regular, 3+g.r.IntN(3)),
		append(g.drafts(regular, 2+g.r.IntN(2)), g.draft(held.Name, pick(g.r, eventTypes), smallPayload)),
	}
	faults := []Fault{"", NATSPaused, PostgresSlowed, DispatcherRestarted}
	n := 0
	for i, drafts := range phases {
		phase := Phase{Fault: faults[i]}
		if phase.Fault == DispatcherRestarted {
			phase.HeldEndpoint = held.Endpoints[0].Name
		}
		for _, d := range drafts {
			n++
			phase.Messages = append(phase.Messages, g.finish(d, fmt.Sprintf("m%03d", n)))
		}
		s.Phases = append(s.Phases, phase)
	}
	return s
}

// generator draws a scenario's choices from one random source, in a fixed
// order, so a seed always gives the same scenario.
type generator struct {
	r *rand.Rand
}

// draft is a Message before it is named. Its payload names it, so the
// payload is made only then.
type draft struct {
	Message
	size  int
	keyed bool
	// sameAs is the earlier Message whose Idempotency-Key this one is sent
	// with: with the same request, or another payload if conflicts is set.
	sameAs    *draft
	conflicts bool
}

func (g *generator) draft(application, eventType string, size int) *draft {
	return &draft{Message: Message{Application: application, EventType: eventType}, size: size}
}

// drafts returns n Messages to the given Applications, with small payloads.
func (g *generator) drafts(apps []Application, n int) []*draft {
	var drafts []*draft
	for range n {
		drafts = append(drafts, g.draft(pick(g.r, apps).Name, pick(g.r, eventTypes), smallPayload))
	}
	return drafts
}

// finish names d and returns its Message, with its payload and key.
func (g *generator) finish(d *draft, name string) Message {
	d.Name = name
	switch {
	case d.sameAs == nil:
		d.Payload = g.payload(name, d.size)
		if d.keyed {
			d.IdempotencyKey = "key-" + name
		}
	case d.conflicts:
		d.Payload = g.payload(name, smallPayload)
		d.IdempotencyKey = d.sameAs.IdempotencyKey
	default:
		// The same request byte for byte, payload included.
		d.Payload, d.IdempotencyKey = d.sameAs.Payload, d.sameAs.IdempotencyKey
	}
	return d.Message
}

// application adds an Application with one Endpoint that behaves as b, and
// returns it.
func (g *generator) application(s *Scenario, b Behaviour) Application {
	name := fmt.Sprintf("app-%d", len(s.Applications)+1)
	app := Application{Name: name, Endpoints: []Endpoint{{Name: name + "/ep-1", Behaviour: b}}}
	s.Applications = append(s.Applications, app)
	return app
}

// filter returns an Endpoint filter, and an Event type it accepts.
func (g *generator) filter() ([]string, string) {
	anchor := pick(g.r, eventTypes)
	prefix, _, _ := strings.Cut(anchor, ".")
	switch g.r.IntN(4) {
	case 0:
		return []string{anchor}, anchor
	case 1:
		return []string{prefix + ".*"}, anchor
	case 2:
		return []string{prefix + ".>"}, anchor
	default:
		return []string{pick(g.r, eventTypes), anchor}, anchor
	}
}

// size returns a payload size: mostly small, sometimes medium, rarely large.
func (g *generator) size() int {
	switch n := g.r.IntN(10); {
	case n < 7:
		return smallPayload
	case n < 9:
		return mediumPayload
	default:
		return largePayload
	}
}

// payload returns a JSON payload of about size bytes, naming its Message,
// whose Endpoints read the name to decide how to answer.
func (g *generator) payload(name string, size int) string {
	// Varies the size within its class, by up to a quarter either way.
	size += size / 4 * (g.r.IntN(3) - 1)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + g.r.IntN(26))
	}
	return fmt.Sprintf(`{"message":%q,"data":%q}`, name, data)
}

// pick returns one of items.
func pick[T any](r *rand.Rand, items []T) T {
	return items[r.IntN(len(items))]
}
