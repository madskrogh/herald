package scenario

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/endpointtest"
)

// The rules CheckEdge checks, in the order it reports them.
const (
	ruleSignature     = "signature"
	ruleWebhookID     = "webhook-id"
	rulePayload       = "payload"
	ruleFanOut        = "fan-out"
	ruleInFlight      = "in flight"
	ruleAfterSuccess  = "after success"
	ruleAttempts      = "attempts"
	ruleRetrySchedule = "retry schedule"
)

// retryScale is the local stack's retry scale, HERALD_RETRY_SCALE in
// compose.yaml, which every retry delay is multiplied by (R-76).
const retryScale = 0.001

// CheckEdge checks the requirements that show in what the Endpoints
// received, against what the scenario sent, one rule each:
//
//   - signature: R-42 and R-43;
//   - webhook-id: R-6;
//   - payload: R-5;
//   - fan-out: R-1 to R-4, by the harness's own matching;
//   - in flight: R-19;
//   - after success: R-9;
//   - attempts: R-12, on the Attempts Herald recorded;
//   - retry schedule: R-11 and R-76, by the harness's own schedule, held to
//     each delay's shortest.
//
// R-17 lets Herald repeat an Attempt it couldn't record, or that shutdown
// cut off. The Attempts a Delivery's Endpoint received beyond those Herald
// recorded are such repeats, and as many as there are may follow a 2xx,
// and as many may come early.
//
// Each rule also fails when the run gives it nothing to check.
func CheckEdge(obs Observations) []Violation {
	e := newEdge(obs)
	e.violations = append(e.violations, e.unplaced...)
	e.checkSignatures()
	e.checkWebhookIDs()
	e.checkPayloads()
	e.checkFanOut()
	e.checkInFlight()
	e.checkAfterSuccess()
	e.checkAttemptCount()
	e.checkRetrySchedule()
	return sortViolations(e.violations, ruleSignature, ruleWebhookID, rulePayload, ruleFanOut, ruleInFlight, ruleAfterSuccess, ruleAttempts, ruleRetrySchedule)
}

// deliveryKey identifies a Delivery by the names of its Message and
// Endpoint.
type deliveryKey struct{ message, endpoint string }

func (k deliveryKey) String() string { return k.message + " to " + k.endpoint }

// edgeDelivery is a Delivery as its Endpoint saw it.
type edgeDelivery struct {
	// attempts are those the Endpoint received, in the order received.
	attempts []endpointtest.Attempt
	// recorded is how many Attempts Herald recorded.
	recorded int
}

// repeats is how many of the Delivery's Attempts Herald made again without
// recording them.
func (d *edgeDelivery) repeats() int {
	return max(0, len(d.attempts)-d.recorded)
}

// edge is a run's Observations arranged for CheckEdge.
type edge struct {
	obs Observations
	// messages holds each Message the scenario sent, by name, and ids the
	// ID of each one Herald accepted.
	messages map[string]Message
	ids      map[string]uuid.UUID
	// names maps each accepted Message's ID to the name of the Message
	// that created it.
	names map[uuid.UUID]string
	// deliveries holds every Delivery that Herald stored or an Endpoint
	// received an Attempt for.
	deliveries map[deliveryKey]*edgeDelivery
	// stored holds the Deliveries Herald stored.
	stored map[deliveryKey]bool
	// byEndpoint holds each Endpoint's Attempts, in the order received.
	byEndpoint map[string][]endpointtest.Attempt
	// unplaced are the Attempts newEdge could place at no Delivery, which
	// break a rule CheckEdge reports.
	unplaced   []Violation
	violations []Violation
}

func newEdge(obs Observations) *edge {
	e := &edge{
		obs:        obs,
		messages:   map[string]Message{},
		ids:        map[string]uuid.UUID{},
		names:      map[uuid.UUID]string{},
		deliveries: map[deliveryKey]*edgeDelivery{},
		stored:     map[deliveryKey]bool{},
		byEndpoint: map[string][]endpointtest.Attempt{},
	}
	// The same request sent again is meant to return the original Message,
	// not create one of its own; CheckFinalState checks that it did.
	again := map[string]bool{}
	for _, r := range keyedRepeats(obs) {
		again[r.repeat.Name] = r.sameRequest()
	}
	for _, phase := range obs.Scenario.Phases {
		for _, m := range phase.Messages {
			e.messages[m.Name] = m
			sent, ok := obs.Sent[m.Name]
			if !ok || sent.Status != 0 {
				continue
			}
			e.ids[m.Name] = sent.ID
			if again[m.Name] {
				continue
			}
			if _, ok := e.names[sent.ID]; !ok {
				e.names[sent.ID] = m.Name
			}
		}
	}
	// heraldByEndpoint lists the Deliveries Herald stored for each
	// Endpoint, to place TLS handshakes, which carry no webhook-id.
	heraldByEndpoint := map[string][]deliveryKey{}
	for _, d := range obs.Deliveries {
		k := deliveryKey{e.names[d.MessageID], obs.EndpointIDs[d.EndpointID]}
		e.stored[k] = true
		e.delivery(k).recorded = obs.Recorded[d.ID]
		heraldByEndpoint[k.endpoint] = append(heraldByEndpoint[k.endpoint], k)
	}
	// unplacedHandshakes counts the TLS handshakes at each Endpoint that
	// can't be placed at a Delivery.
	unplacedHandshakes := map[string]int{}
	attempts := slices.Clone(obs.Attempts)
	slices.SortStableFunc(attempts, func(a, b endpointtest.Attempt) int { return a.ReceivedAt.Compare(b.ReceivedAt) })
	for _, a := range attempts {
		endpoint, ok := obs.EndpointURLs[a.EndpointURL]
		if !ok {
			e.unplace(ruleFanOut, a.EndpointURL, "received an Attempt, but the scenario created no Endpoint there")
			continue
		}
		e.byEndpoint[endpoint] = append(e.byEndpoint[endpoint], a)
		var k deliveryKey
		switch {
		case a.Handshake:
			ks := heraldByEndpoint[endpoint]
			if len(ks) != 1 {
				unplacedHandshakes[endpoint]++
				continue
			}
			k = ks[0]
		default:
			message, ok := e.messageOf(a)
			if !ok {
				e.unplace(ruleWebhookID, endpoint, fmt.Sprintf("an Attempt with webhook-id %q and a body that name no Message the scenario sent", a.WebhookID))
				continue
			}
			k = deliveryKey{message, endpoint}
		}
		d := e.delivery(k)
		d.attempts = append(d.attempts, a)
	}
	for endpoint, n := range unplacedHandshakes {
		e.unplace(ruleFanOut, endpoint, fmt.Sprintf("%d TLS handshakes, which can be placed only at an Endpoint with one Delivery, not %d", n, len(heraldByEndpoint[endpoint])))
	}
	return e
}

// delivery returns the Delivery k, adding it if it is new.
func (e *edge) delivery(k deliveryKey) *edgeDelivery {
	d, ok := e.deliveries[k]
	if !ok {
		d = &edgeDelivery{}
		e.deliveries[k] = d
	}
	return d
}

// messageOf returns the name of the Message an Attempt is for: the one its
// body names, as every scenario payload does, or else the one whose ID is
// its webhook-id. Either may be what broke a rule, and the rules compare
// the Attempt with the Message.
func (e *edge) messageOf(a endpointtest.Attempt) (string, bool) {
	var p struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(a.Body, &p) == nil {
		if _, ok := e.ids[p.Message]; ok {
			return e.names[e.ids[p.Message]], true
		}
	}
	id, err := uuid.Parse(a.WebhookID)
	if err != nil {
		return "", false
	}
	name, ok := e.names[id]
	return name, ok
}

func (e *edge) unplace(rule, subject, detail string) {
	e.unplaced = append(e.unplaced, Violation{Rule: rule, Subject: subject, Detail: detail})
}

func (e *edge) add(rule, subject, detail string) {
	e.violations = append(e.violations, Violation{Rule: rule, Subject: subject, Detail: detail})
}

// nothingToCheck reports a rule that the run gave nothing to check.
func (e *edge) nothingToCheck(rule, what string) {
	e.add(rule, "", "nothing to check: the run has no "+what)
}

// eachDelivery calls f for each Delivery, in order of name.
func (e *edge) eachDelivery(f func(k deliveryKey, d *edgeDelivery)) {
	keys := make([]deliveryKey, 0, len(e.deliveries))
	for k := range e.deliveries {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b deliveryKey) int { return strings.Compare(a.String(), b.String()) })
	for _, k := range keys {
		f(k, e.deliveries[k])
	}
}

// eachHTTPAttempt calls f for each Attempt that got past the TLS handshake, with
// its number among its Delivery's Attempts, and returns how many there
// were.
func (e *edge) eachHTTPAttempt(f func(k deliveryKey, n int, a endpointtest.Attempt)) int {
	count := 0
	e.eachDelivery(func(k deliveryKey, d *edgeDelivery) {
		for i, a := range d.attempts {
			if !a.Handshake {
				count++
				f(k, i+1, a)
			}
		}
	})
	return count
}

// checkEachHTTPAttempt reports each Delivery for the first of its Attempts that
// broken says breaks rule, with why.
func (e *edge) checkEachHTTPAttempt(rule string, broken func(k deliveryKey, a endpointtest.Attempt) string) {
	reported := map[deliveryKey]bool{}
	n := e.eachHTTPAttempt(func(k deliveryKey, n int, a endpointtest.Attempt) {
		if reported[k] {
			return
		}
		if why := broken(k, a); why != "" {
			reported[k] = true
			e.add(rule, k.String(), fmt.Sprintf("Attempt %d: %s", n, why))
		}
	})
	if n == 0 {
		e.nothingToCheck(rule, "Attempts past the TLS handshake")
	}
}

func (e *edge) checkSignatures() {
	e.checkEachHTTPAttempt(ruleSignature, func(_ deliveryKey, a endpointtest.Attempt) string {
		if a.SignatureErr != nil {
			return "the signature doesn't verify with the Endpoint's signing secret: " + a.SignatureErr.Error()
		}
		return ""
	})
}

func (e *edge) checkWebhookIDs() {
	e.checkEachHTTPAttempt(ruleWebhookID, func(k deliveryKey, a endpointtest.Attempt) string {
		if want := e.ids[k.message].String(); a.WebhookID != want {
			return fmt.Sprintf("webhook-id %q, want the Message's ID %s", a.WebhookID, want)
		}
		return ""
	})
	for endpoint, attempts := range e.byEndpoint {
		messages := map[string][]string{}
		for _, a := range attempts {
			if a.Handshake {
				continue
			}
			if m, ok := e.messageOf(a); ok && !slices.Contains(messages[a.WebhookID], m) {
				messages[a.WebhookID] = append(messages[a.WebhookID], m)
			}
		}
		for id, ms := range messages {
			if len(ms) > 1 {
				slices.Sort(ms)
				e.add(ruleWebhookID, endpoint, fmt.Sprintf("Messages %s share webhook-id %s", strings.Join(ms, ", "), id))
			}
		}
	}
}

func (e *edge) checkPayloads() {
	e.checkEachHTTPAttempt(rulePayload, func(k deliveryKey, a endpointtest.Attempt) string {
		payload := e.messages[k.message].Payload
		if string(a.Body) != payload {
			return fmt.Sprintf("a %d-byte body that isn't the Message's %d-byte payload", len(a.Body), len(payload))
		}
		return ""
	})
}

// checkFanOut compares the Deliveries the Endpoints received with those the
// scenario's filters call for. The scenario's record of what it sent is
// the source of truth, not Herald's Deliveries.
func (e *edge) checkFanOut() {
	for _, app := range e.obs.Scenario.Applications {
		for _, ep := range app.Endpoints {
			if err := filterError(ep.Filter); err != nil {
				e.add(ruleFanOut, ep.Name, fmt.Sprintf("the scenario's filter %q doesn't parse: %v", ep.Filter, err))
			}
		}
	}
	for id, name := range e.names {
		m := e.messages[name]
		if err := eventTypeError(m.EventType); err != nil {
			e.add(ruleFanOut, name, fmt.Sprintf("Herald accepted Message %s with Event type %q, which doesn't parse: %v", id, m.EventType, err))
		}
	}
	want := e.matching()
	if len(want) == 0 {
		e.nothingToCheck(ruleFanOut, "accepted Messages that match an Endpoint")
	}
	e.eachDelivery(func(k deliveryKey, d *edgeDelivery) {
		switch received := len(d.attempts) > 0; {
		case want[k] && !received:
			e.add(ruleFanOut, k.String(), "the Endpoint's filter matches the Message's Event type, but it received no Attempt")
		case !want[k] && received:
			e.add(ruleFanOut, k.String(), fmt.Sprintf("the Endpoint received %d Attempts, but its filter doesn't match Event type %s", len(d.attempts), e.messages[k.message].EventType))
		}
	})
	for k := range want {
		if _, ok := e.deliveries[k]; !ok {
			e.add(ruleFanOut, k.String(), "the Endpoint's filter matches the Message's Event type, but it received no Attempt")
		}
	}
}

// matching returns the Deliveries the scenario's filters call for: one for
// each accepted Message and each Endpoint of its Application whose filter
// matches its Event type. A filter or Event type that doesn't parse
// matches nothing.
func (e *edge) matching() map[deliveryKey]bool {
	filters := map[string][]Endpoint{}
	for _, app := range e.obs.Scenario.Applications {
		filters[app.Name] = app.Endpoints
	}
	want := map[deliveryKey]bool{}
	for _, name := range e.names {
		m := e.messages[name]
		if eventTypeError(m.EventType) != nil {
			continue
		}
		for _, ep := range filters[m.Application] {
			if filterError(ep.Filter) == nil && matches(ep.Filter, m.EventType) {
				want[deliveryKey{name, ep.Name}] = true
			}
		}
	}
	return want
}

// succeeded reports whether an Endpoint answered a with a 2xx.
func succeeded(a endpointtest.Attempt) bool {
	return a.Answer.Kind == endpointtest.AnswerResponse && a.Answer.Status >= 200 && a.Answer.Status < 300
}

// checkInFlight counts each Endpoint's Attempts in flight, from when it
// received each to when it answered. An Attempt it never answered is in
// flight to the end.
func (e *edge) checkInFlight() {
	if len(e.byEndpoint) == 0 {
		e.nothingToCheck(ruleInFlight, "Attempts")
	}
	for endpoint, attempts := range e.byEndpoint {
		type event struct {
			at    time.Time
			delta int
		}
		var events []event
		for _, a := range attempts {
			events = append(events, event{a.ReceivedAt, 1})
			if !a.AnsweredAt.IsZero() {
				events = append(events, event{a.AnsweredAt, -1})
			}
		}
		// At the same moment, an Attempt that ends makes room for one that
		// starts.
		slices.SortFunc(events, func(a, b event) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.delta, b.delta)) })
		inFlight, most := 0, 0
		var at time.Time
		for _, ev := range events {
			inFlight += ev.delta
			if inFlight > most {
				most, at = inFlight, ev.at
			}
		}
		if most > maxInFlight {
			e.add(ruleInFlight, endpoint, fmt.Sprintf("%d Attempts in flight at %s, want at most %d", most, at.Format(time.RFC3339Nano), maxInFlight))
		}
	}
}

func (e *edge) checkAfterSuccess() {
	checked := 0
	e.eachDelivery(func(k deliveryKey, d *edgeDelivery) {
		i := slices.IndexFunc(d.attempts, succeeded)
		if i < 0 {
			return
		}
		checked++
		if after := len(d.attempts) - i - 1; after > d.repeats() {
			e.add(ruleAfterSuccess, k.String(), fmt.Sprintf("%d Attempts after Attempt %d got %d, of which at most %d are repeats Herald didn't record", after, i+1, d.attempts[i].Answer.Status, d.repeats()))
		}
	})
	if checked == 0 {
		e.nothingToCheck(ruleAfterSuccess, "Deliveries with a 2xx")
	}
}

func (e *edge) checkAttemptCount() {
	if len(e.stored) == 0 {
		e.nothingToCheck(ruleAttempts, "Deliveries that Herald stored")
	}
	e.eachDelivery(func(k deliveryKey, d *edgeDelivery) {
		if e.stored[k] && d.recorded > maxAttempts {
			e.add(ruleAttempts, k.String(), fmt.Sprintf("Herald recorded %d Attempts, want at most %d", d.recorded, maxAttempts))
		}
	})
}

// checkRetrySchedule checks that each Attempt came no sooner after the one
// before it was answered than the retry schedule allows. Herald's clock
// starts the delay once it has the answer, a moment after the Endpoint
// wrote it, so the Endpoint's answer is a lower bound. A repeat Herald
// didn't record shifts the Attempts after it by one, so with repeats each
// Attempt is held to the shortest delay it can have.
func (e *edge) checkRetrySchedule() {
	checked := 0
	e.eachDelivery(func(k deliveryKey, d *edgeDelivery) {
		if len(d.attempts) < 2 {
			return
		}
		checked++
		repeats := d.repeats()
		early := 0
		var first string
		for i := 1; i < len(d.attempts); i++ {
			prev, a := d.attempts[i-1], d.attempts[i]
			end := prev.AnsweredAt
			if end.IsZero() {
				end = prev.ReceivedAt
			}
			// Attempt i+1 follows i Attempts, of which up to repeats weren't
			// recorded.
			earliest, _ := retryWindow(min(i-repeats, maxAttempts-1), retryScale)
			if gap := a.ReceivedAt.Sub(end); gap < earliest {
				early++
				if first == "" {
					first = fmt.Sprintf("Attempt %d came %v after Attempt %d was answered, want at least %v", i+1, gap, i, earliest)
				}
			}
		}
		if early > repeats {
			e.add(ruleRetrySchedule, k.String(), fmt.Sprintf("%s; %d Attempts came early, of which at most %d are repeats Herald didn't record", first, early, repeats))
		}
	})
	if checked == 0 {
		e.nothingToCheck(ruleRetrySchedule, "Deliveries with more than one Attempt")
	}
}
