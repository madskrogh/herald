package scenario

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/e2e"
)

// The rules CheckSpans checks, in the order it reports them.
const (
	ruleTrace    = "trace"
	ruleParent   = "parent"
	ruleSchema   = "schema"
	ruleError    = "error"
	ruleLag      = "lag"
	ruleLateness = "lateness"
)

// The limits on handoff lag and how late an Attempt starts: tight on the
// normal path, and loose enough under a fault that only a stall breaks
// them. Neither depends on the retry scale: the relay's and the workers'
// polling drive both (decided in #80).
const (
	delayLimit        = 2 * time.Second
	delayLimitInFault = 15 * time.Second
)

// The span names, attribute keys and outcomes the rules read, as the span
// schema in the spec names them (spec/span-schema.md).
const (
	spanTenantAPICall   = "tenant_api.call"
	spanHandoffBatch    = "handoff.batch"
	spanHandoff         = "handoff"
	spanStoreDeliveries = "store_deliveries"
	spanAttempt         = "attempt"

	attrMessageID  = "herald.message_id"
	attrEndpointID = "herald.endpoint_id"
	attrDeliveryID = "herald.delivery_id"
	attrAttempt    = "herald.attempt"
	attrOutcome    = "herald.outcome"
	attrStatusCode = "herald.status_code"
	attrLatenessMs = "herald.lateness_ms"
	attrError      = "herald.error"

	outcomeHandedOff   = "handed_off"
	outcomeStored      = "stored"
	outcomeFailed      = "failed"
	outcomeSucceeded   = "succeeded"
	outcomePending     = "pending"
	outcomeCutOff      = "cut_off"
	outcomeNotRecorded = "not_recorded"
)

// statusError is the status of a span whose work failed.
const statusError = "Error"

// endpointErrors says which errors an Attempt to an Endpoint with each
// behaviour may record: its status code, or, for 0, a part of its error
// that says why there was no response. Other behaviours answer every
// Attempt in time.
var endpointErrors = map[Behaviour]struct {
	status int
	errors []string
}{
	Fail:      {status: 500},
	Flap:      {status: 503},
	Redirect:  {status: 302},
	TimeOut:   {errors: []string{"deadline exceeded"}},
	Reset:     {errors: []string{"EOF", "connection reset"}},
	Malformed: {errors: []string{"malformed HTTP"}},
	TLSError:  {errors: []string{"certificate"}},
}

// faultErrors says which faults may make a span's work fail, by span name,
// beyond what an Endpoint causes. A paused NATS, a slowed Postgres and a
// dispatcher shutting down all fail handoff rounds and the storing of
// Batches.
var faultErrors = map[string][]Fault{
	spanHandoff:         {NATSPaused, PostgresSlowed, DispatcherRestarted},
	spanHandoffBatch:    {NATSPaused, PostgresSlowed, DispatcherRestarted},
	spanStoreDeliveries: {NATSPaused, PostgresSlowed, DispatcherRestarted},
}

// The Tenant API call's outcome and error for a Message sent again with an
// Idempotency-Key and another request, which the scenario sends on purpose.
const (
	outcomeFailedPrecondition = "FailedPrecondition"
	idempotencyConflict       = "Idempotency-Key was used for a different request"
)

// CheckSpans checks the rules that hold in a run's spans: every span of
// each trace that has a span of the run's Tenant, and the Batches that link
// to them.
//
//   - every Message Herald accepted has one trace from its Tenant API call
//     through the handoff and the storing of its Deliveries to a span for
//     every Attempt Herald recorded;
//   - every span's parent is in its trace, and every root is a Tenant API
//     call or a Batch;
//   - every span follows the span schema: its name, its attributes and their
//     types, its outcome, its service, and the spans it is a child of and
//     links to;
//   - no span records an error the scenario doesn't cause: an Endpoint's,
//     through its behaviour, or a fault's, in the fault's phase;
//   - each handoff comes within 2 s of its Tenant API call, and each Attempt
//     starts within 2 s of falling due, or 15 s under a fault.
//
// Each rule also fails when the run gives it nothing to check, and a span
// without an attribute a rule reads breaks the rule.
func CheckSpans(obs Observations, spans []e2e.Span) []Violation {
	c := newSpanCheck(obs, spans)
	c.checkTraces()
	c.checkParents()
	c.checkSchema()
	c.checkErrors()
	c.checkLag()
	c.checkLateness()
	return sortViolations(c.violations, ruleTrace, ruleParent, ruleSchema, ruleError, ruleLag, ruleLateness)
}

// spanCheck is a run's spans arranged for CheckSpans.
type spanCheck struct {
	*edge
	spans []e2e.Span
	// faults holds the fault of the phase each Message was sent in, by the
	// Message's ID.
	faults map[string]Fault
	// behaviours holds each Endpoint's behaviour, by its ID.
	behaviours map[string]Behaviour
	// byTrace holds each trace's spans by ID.
	byTrace map[string]map[string]e2e.Span
}

func newSpanCheck(obs Observations, spans []e2e.Span) *spanCheck {
	c := &spanCheck{
		edge:       newEdge(obs),
		spans:      slices.Clone(spans),
		faults:     map[string]Fault{},
		behaviours: map[string]Behaviour{},
		byTrace:    map[string]map[string]e2e.Span{},
	}
	slices.SortStableFunc(c.spans, func(a, b e2e.Span) int {
		return cmp.Or(a.Start.Compare(b.Start), strings.Compare(a.SpanID, b.SpanID))
	})
	for _, phase := range obs.Scenario.Phases {
		for _, m := range phase.Messages {
			if id, ok := c.ids[m.Name]; ok {
				if _, ok := c.faults[id.String()]; !ok {
					c.faults[id.String()] = phase.Fault
				}
			}
		}
	}
	behaviours := map[string]Behaviour{}
	for _, app := range obs.Scenario.Applications {
		for _, ep := range app.Endpoints {
			behaviours[ep.Name] = ep.Behaviour
		}
	}
	for id, name := range obs.EndpointIDs {
		c.behaviours[id.String()] = behaviours[name]
	}
	for _, s := range c.spans {
		if c.byTrace[s.TraceID] == nil {
			c.byTrace[s.TraceID] = map[string]e2e.Span{}
		}
		c.byTrace[s.TraceID][s.SpanID] = s
	}
	return c
}

// subject names a span in a Violation.
func subject(s e2e.Span) string {
	return s.Name + " span " + s.SpanID
}

// root returns the Tenant API call at the root of a trace.
func (c *spanCheck) root(trace string) (e2e.Span, bool) {
	for _, s := range c.byTrace[trace] {
		if s.Name == spanTenantAPICall && s.ParentSpanID == "" {
			return s, true
		}
	}
	return e2e.Span{}, false
}

// handedOff returns the span of the handoff that handed off the Message
// with the given ID.
func (c *spanCheck) handedOff(messageID string) (e2e.Span, bool) {
	for _, s := range c.spans {
		if s.Name == spanHandoff && s.Attributes[attrMessageID] == messageID &&
			s.Attributes[attrOutcome] == outcomeHandedOff {
			return s, true
		}
	}
	return e2e.Span{}, false
}

// messages returns the IDs of the Messages Herald accepted, in order of
// name.
func (c *spanCheck) messages() []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(c.names))
	for id := range c.names {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(c.names[a], c.names[b]) })
	return ids
}

func (c *spanCheck) checkTraces() {
	if len(c.names) == 0 {
		c.nothingToCheck(ruleTrace, "accepted Messages")
	}
	deliveries := map[uuid.UUID][]delivery.Delivery{}
	for _, d := range c.obs.Deliveries {
		deliveries[d.MessageID] = append(deliveries[d.MessageID], d)
	}
	for _, id := range c.messages() {
		if why := c.missingFromTrace(id.String(), deliveries[id]); why != "" {
			c.add(ruleTrace, c.names[id], why)
		}
	}
}

// missingFromTrace says what the trace of the Message with the given ID
// lacks, or returns empty if it has everything.
func (c *spanCheck) missingFromTrace(messageID string, deliveries []delivery.Delivery) string {
	handoff, ok := c.handedOff(messageID)
	if !ok {
		return "no handoff span that handed the Message off"
	}
	trace := c.byTrace[handoff.TraceID]
	if _, ok := c.root(handoff.TraceID); !ok {
		return fmt.Sprintf("its trace %s has no Tenant API call at its root", handoff.TraceID)
	}
	recorded := map[string]bool{}
	stored := false
	for _, s := range trace {
		switch {
		case s.Name == spanStoreDeliveries && s.Attributes[attrMessageID] == messageID &&
			s.Attributes[attrOutcome] == outcomeStored:
			stored = true
		// An Attempt Herald couldn't record may be recorded all the same:
		// when the commit times out, Postgres may have committed it.
		case s.Name == spanAttempt && slices.Contains(
			[]string{outcomeSucceeded, outcomePending, outcomeFailed, outcomeNotRecorded},
			s.Attributes[attrOutcome]):
			recorded[s.Attributes[attrDeliveryID]+"/"+s.Attributes[attrAttempt]] = true
		}
	}
	if !stored {
		return fmt.Sprintf("its trace %s has no span that stored its Deliveries", handoff.TraceID)
	}
	for _, d := range deliveries {
		for n := range c.obs.Recorded[d.ID] {
			if !recorded[fmt.Sprintf("%s/%d", d.ID, n+1)] {
				return fmt.Sprintf("its trace %s has no span for Attempt %d of Delivery %s, which Herald recorded", handoff.TraceID, n+1, d.ID)
			}
		}
	}
	return ""
}

func (c *spanCheck) checkParents() {
	if len(c.spans) == 0 {
		c.nothingToCheck(ruleParent, "spans")
	}
	for _, s := range c.spans {
		switch {
		case s.ParentSpanID != "":
			if _, ok := c.byTrace[s.TraceID][s.ParentSpanID]; !ok {
				c.add(ruleParent, subject(s), fmt.Sprintf("its parent %s isn't in its trace %s", s.ParentSpanID, s.TraceID))
			}
		case s.Name != spanTenantAPICall && s.Name != spanHandoffBatch:
			c.add(ruleParent, subject(s), fmt.Sprintf("a root in trace %s, but only a Tenant API call or a Batch starts a trace", s.TraceID))
		}
	}
}

func (c *spanCheck) checkSchema() {
	if len(c.spans) == 0 {
		c.nothingToCheck(ruleSchema, "spans")
		return
	}
	problems, err := e2e.SchemaProblems(c.spans)
	if err != nil {
		c.add(ruleSchema, "", err.Error())
		return
	}
	for _, s := range c.spans {
		if ps := problems[s.SpanID]; len(ps) > 0 {
			c.add(ruleSchema, subject(s), errors.Join(ps...).Error())
		}
	}
}

func (c *spanCheck) checkErrors() {
	if len(c.spans) == 0 {
		c.nothingToCheck(ruleError, "spans")
	}
	conflicts := c.idempotencyConflicts()
	for _, s := range c.spans {
		msg, ok := s.Attributes[attrError]
		switch {
		case !ok:
			c.add(ruleError, subject(s), "no "+attrError+" attribute")
			continue
		case msg == "" && s.StatusCode != statusError:
			continue
		case msg == "":
			c.add(ruleError, subject(s), "an error status, but no error")
			continue
		}
		if s.Name == spanTenantAPICall && s.Attributes[attrOutcome] == outcomeFailedPrecondition &&
			strings.HasPrefix(msg, idempotencyConflict) && conflicts > 0 {
			conflicts--
			continue
		}
		if !c.expected(s) {
			detail := msg
			if m, ok := c.messageName(s); ok {
				detail = "Message " + m + ": " + msg
			}
			c.add(ruleError, subject(s), detail)
		}
	}
}

// idempotencyConflicts is how many Messages the scenario sent again with an
// Idempotency-Key and another request, and the Tenant API refused.
func (c *spanCheck) idempotencyConflicts() int {
	n := 0
	for _, r := range keyedRepeats(c.obs) {
		if !r.sameRequest() && c.obs.Sent[r.repeat.Name].Status == http.StatusUnprocessableEntity {
			n++
		}
	}
	return n
}

// expected reports whether the scenario causes the error s records.
func (c *spanCheck) expected(s e2e.Span) bool {
	switch s.Name {
	case spanAttempt:
		fault := c.faults[s.Attributes[attrMessageID]]
		switch s.Attributes[attrOutcome] {
		case outcomeCutOff:
			return fault == DispatcherRestarted
		case outcomeNotRecorded:
			return fault == PostgresSlowed
		}
		want, ok := endpointErrors[c.behaviours[s.Attributes[attrEndpointID]]]
		if !ok {
			return false
		}
		status, _ := strconv.Atoi(s.Attributes[attrStatusCode])
		if status != 0 {
			return status == want.status
		}
		return slices.ContainsFunc(want.errors, func(e string) bool {
			return strings.Contains(s.Attributes[attrError], e)
		})
	case spanHandoffBatch:
		if s.Attributes[attrOutcome] != outcomeFailed {
			return false
		}
		// A Batch is in no Message's phase, but holds Messages of one: the
		// phases run one after another.
		for _, link := range s.Links {
			for _, h := range c.spans {
				if h.SpanID == link.SpanID && slices.Contains(faultErrors[s.Name], c.faults[h.Attributes[attrMessageID]]) {
					return true
				}
			}
		}
		return false
	default:
		return s.Attributes[attrOutcome] == outcomeFailed &&
			slices.Contains(faultErrors[s.Name], c.faults[s.Attributes[attrMessageID]])
	}
}

// messageName returns the name of the Message whose span s is.
func (c *spanCheck) messageName(s e2e.Span) (string, bool) {
	id, err := uuid.Parse(s.Attributes[attrMessageID])
	if err != nil {
		return "", false
	}
	name, ok := c.names[id]
	return name, ok
}

// limit returns the limit on lag and lateness for the Message with the
// given ID.
func (c *spanCheck) limit(messageID string) time.Duration {
	if c.faults[messageID] != "" {
		return delayLimitInFault
	}
	return delayLimit
}

func (c *spanCheck) checkLag() {
	checked := 0
	for _, id := range c.messages() {
		handoff, ok := c.handedOff(id.String())
		if !ok {
			continue
		}
		call, ok := c.root(handoff.TraceID)
		if !ok {
			continue
		}
		checked++
		if lag, limit := handoff.Start.Sub(call.End), c.limit(id.String()); lag > limit {
			c.add(ruleLag, c.names[id], fmt.Sprintf("handed off %v after its Tenant API call, want within %v", lag, limit))
		}
	}
	if checked == 0 {
		c.nothingToCheck(ruleLag, "handoffs in a trace with a Tenant API call")
	}
}

func (c *spanCheck) checkLateness() {
	checked := 0
	reported := map[string]bool{}
	for _, s := range c.spans {
		message, ok := c.messageName(s)
		if s.Name != spanAttempt || !ok {
			continue
		}
		checked++
		endpoint := s.Attributes[attrEndpointID]
		if id, err := uuid.Parse(endpoint); err == nil {
			endpoint = c.obs.EndpointIDs[id]
		}
		k := deliveryKey{message, endpoint}.String()
		if reported[k] {
			continue
		}
		ms, err := strconv.ParseInt(s.Attributes[attrLatenessMs], 10, 64)
		if err != nil {
			reported[k] = true
			c.add(ruleLateness, k, fmt.Sprintf("Attempt span %s has no lateness: %s = %q", s.SpanID, attrLatenessMs, s.Attributes[attrLatenessMs]))
			continue
		}
		if late, limit := time.Duration(ms)*time.Millisecond, c.limit(s.Attributes[attrMessageID]); late > limit {
			reported[k] = true
			c.add(ruleLateness, k, fmt.Sprintf("Attempt %s started %v after it fell due, want within %v", s.Attributes[attrAttempt], late, limit))
		}
	}
	if checked == 0 {
		c.nothingToCheck(ruleLateness, "Attempt spans")
	}
}
