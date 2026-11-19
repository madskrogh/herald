package scenario

import (
	"fmt"
	"math/rand/v2"
	"slices"
)

// eventTypes are the Event types most Messages are sent with, which only the
// Endpoints that accept every Event type receive.
var eventTypes = []string{
	"invoice.created", "invoice.paid", "invoice.voided",
	"customer.created", "customer.updated", "customer.deleted",
	"payout.sent", "payout.failed",
}

// Every Endpoint with a filter gets Event types of its own: one of these
// prefixes, which no other Endpoint has, with one of the suffixes. That
// keeps its Deliveries few, and counted.
var (
	filterPrefixes = []string{"refund", "dispute", "subscription", "transfer", "charge", "order", "shipment", "coupon"}
	filterSuffixes = []string{"created", "updated", "completed", "failed"}
)

// maxDeliveries is the most Deliveries an Endpoint whose Deliveries take
// more than one Attempt, or whose Attempts take seconds, gets in a phase:
// the 4 Attempts an Endpoint may have in flight (maxInFlight in
// internal/delivery). With more, one Delivery's Attempts would wait for
// another's, and their timing would vary from run to run.
const maxDeliveries = 4

// Fast Endpoints answer within milliseconds. Those that accept every Event
// type answer every Attempt at once, so they can take many Deliveries; the
// others get Event types of their own, so they get few.
var (
	acceptAll = []Behaviour{Succeed, Succeed, Oversized}
	filtered  = []Behaviour{Fail, Reset, Malformed, Flap}
)

// slowEndpoints are the behaviours whose Attempts take seconds, each given an
// Application of its own with one Endpoint, and how many Messages it gets at
// most: one for TLSError, whose handshakes carry no webhook-id to tell
// Deliveries apart.
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

	// Applications whose Endpoints answer fast. The first Endpoint of each
	// accepts every Event type, so every Message has a Delivery.
	regular := make([]Application, 3+g.r.IntN(3))
	for i := range regular {
		regular[i] = newApplication(i+1, pick(g.r, acceptAll))
	}
	// At least one of each, whatever the draws.
	i := g.r.IntN(len(regular))
	regular[i].Endpoints[0].Behaviour = Oversized
	regular[(i+1)%len(regular)].Endpoints[0].Behaviour = Succeed

	// The other fast Endpoints have every filtered behaviour between them,
	// each with Event types of its own and a few Messages of those types.
	behaviours := slices.Clone(filtered)
	for range 1 + g.r.IntN(3) {
		behaviours = append(behaviours, pick(g.r, filtered))
	}
	prefixes := slices.Clone(filterPrefixes)
	g.r.Shuffle(len(prefixes), func(i, j int) { prefixes[i], prefixes[j] = prefixes[j], prefixes[i] })
	var steady []*draft
	var filteredEndpoints []filteredEndpoint
	for i, b := range behaviours {
		app := &regular[g.r.IntN(len(regular))]
		ep := Endpoint{Name: fmt.Sprintf("%s/ep-%d", app.Name, len(app.Endpoints)+1), Behaviour: b}
		fe := filteredEndpoint{application: app.Name}
		ep.Filter, fe.eventTypes = g.filter(prefixes[i])
		app.Endpoints = append(app.Endpoints, ep)
		filteredEndpoints = append(filteredEndpoints, fe)
		for range 1 + g.r.IntN(maxDeliveries) {
			steady = append(steady, g.draftFor(fe, g.size()))
		}
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
		g.faultDrafts(regular, filteredEndpoints),
		// While Postgres is slowed, a dispatcher may make an Attempt again,
		// when it couldn't record it in time. A filtered Endpoint would
		// answer the later Attempts differently, as it counts them.
		g.faultDrafts(regular, nil),
		append(g.faultDrafts(regular, filteredEndpoints), g.draft(held.Name, pick(g.r, eventTypes), smallPayload)),
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

// filteredEndpoint is an Endpoint with a filter: its Application and the Event types
// it accepts.
type filteredEndpoint struct {
	application string
	eventTypes  []string
}

// draftFor returns a Message that reaches ep, with a payload of about size
// bytes.
func (g *generator) draftFor(ep filteredEndpoint, size int) *draft {
	return g.draft(ep.application, pick(g.r, ep.eventTypes), size)
}

// faultDrafts returns the Messages of a phase with a fault, with small
// payloads: a few to the given Applications, and one to one of eps, if
// there are any.
func (g *generator) faultDrafts(apps []Application, eps []filteredEndpoint) []*draft {
	var drafts []*draft
	for range 3 + g.r.IntN(2) {
		drafts = append(drafts, g.draft(pick(g.r, apps).Name, pick(g.r, eventTypes), smallPayload))
	}
	if len(eps) == 0 {
		return drafts
	}
	return append(drafts, g.draftFor(pick(g.r, eps), smallPayload))
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
	app := newApplication(len(s.Applications)+1, b)
	s.Applications = append(s.Applications, app)
	return app
}

// newApplication returns the nth Application, with one Endpoint that
// accepts every Event type and behaves as b.
func newApplication(n int, b Behaviour) Application {
	name := fmt.Sprintf("app-%d", n)
	return Application{Name: name, Endpoints: []Endpoint{{Name: name + "/ep-1", Behaviour: b}}}
}

// filter returns an Endpoint filter on Event types with the given prefix,
// and the Event types it accepts.
func (g *generator) filter(prefix string) ([]string, []string) {
	all := make([]string, len(filterSuffixes))
	for i, s := range filterSuffixes {
		all[i] = prefix + "." + s
	}
	switch g.r.IntN(4) {
	case 0:
		one := pick(g.r, all)
		return []string{one}, []string{one}
	case 1:
		return []string{prefix + ".*"}, all
	case 2:
		return []string{prefix + ".>"}, all
	default:
		perm := g.r.Perm(len(all))
		two := []string{all[perm[0]], all[perm[1]]}
		return two, two
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
