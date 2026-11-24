package scenario

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
)

// Observations are what a run of a Scenario observed, before they are
// normalised.
type Observations struct {
	Scenario Scenario
	// TenantID is the ID of the Tenant the run created and sent as.
	TenantID uuid.UUID
	// Sent is what the Tenant API answered to each Message, by name.
	Sent map[string]Sent
	// EndpointURLs names each Endpoint by its URL, and EndpointIDs by its ID.
	EndpointURLs map[string]string
	EndpointIDs  map[uuid.UUID]string
	// Deliveries are the Deliveries of the Messages sent, once each has
	// succeeded or failed.
	Deliveries []e2e.Delivery
	// Recorded is how many Attempts Herald recorded for each Delivery, by
	// the Delivery's ID.
	Recorded map[uuid.UUID]int
	// Attempts are every Attempt the Endpoints received.
	Attempts []endpointtest.Attempt
}

// Sent is what the Tenant API answered to a Message.
type Sent struct {
	// At is when the Message was sent.
	At time.Time
	// ID is the Message's ID, if the Message was accepted.
	ID uuid.UUID
	// Status is the HTTP status code of a refusal, or 0.
	Status int
}

// Recording is a run's Observations normalised: what doesn't vary between
// runs of the same build. It is what the baseline holds.
type Recording struct {
	// Traffic is the traffic file the run ran.
	Traffic TrafficFile `json:"traffic"`
	// Faults has one record for each phase with a fault, in order.
	Faults     []FaultRecord    `json:"faults"`
	Messages   []MessageRecord  `json:"messages"`
	Deliveries []DeliveryRecord `json:"deliveries"`
}

// FaultRecord is how a fault held back the Messages sent under it.
type FaultRecord struct {
	Fault Fault `json:"fault"`
	// Late says some Delivery of the phase's Messages had its first Attempt
	// lateAfter or more after its Message was sent. It is about the phase,
	// not each Delivery: a Message sent as the fault takes hold may escape
	// it in one run and not another.
	Late bool `json:"late"`
}

// MessageRecord is what the Tenant API answered to a Message.
type MessageRecord struct {
	Name string `json:"name"`
	// Outcome is "accepted", "same as <name>" for a repeated request that
	// returned an earlier Message, or "refused: <status code>".
	Outcome string `json:"outcome"`
}

// DeliveryRecord is one Delivery: the Attempts its Endpoint received, in
// order, and the state it ended in.
type DeliveryRecord struct {
	Message   string    `json:"message"`
	Endpoint  string    `json:"endpoint"`
	Behaviour Behaviour `json:"behaviour"`
	// Fault is the fault in effect while the Message was sent.
	Fault    Fault             `json:"fault,omitempty"`
	State    e2e.DeliveryState `json:"state"`
	Attempts []AttemptRecord   `json:"attempts"`
}

// AttemptRecord is one Attempt an Endpoint received.
type AttemptRecord struct {
	// AfterMs is the time since the Delivery's first Attempt, in
	// milliseconds. It is left out under a fault, whose timing depends on
	// the machine.
	AfterMs *int64 `json:"after_ms,omitempty"`
	// Handshake says the Attempt got no further than the TLS handshake.
	Handshake bool `json:"handshake,omitempty"`
	// Header holds the request's headers, each with its values joined. The
	// timestamp and signature are replaced, and the webhook-id is the
	// Message's name.
	Header map[string]string `json:"header,omitempty"`
	// Body is "the payload of <name>" when it is that Message's payload,
	// and otherwise its size and SHA-256.
	Body string `json:"body,omitempty"`
	// Signature is "verified", or why it didn't verify.
	Signature string `json:"signature,omitempty"`
	// Answer is how the Endpoint answered.
	Answer string `json:"answer"`
}

// lateAfter is how long after its Message was sent a Delivery's first
// Attempt comes, at least, when a fault held it back: more than without a
// fault, and less than any fault holds a Message back.
//
// It is a fact about the scenario's faults, not a decision about Herald, so
// it isn't in the spec: every build is held back by a paused NATS. A fault
// phase recorded as late shows that the fault took hold, so fault injection
// that stopped working shows up in the comparison rather than passing
// unnoticed (decided on #112).
const lateAfter = 3 * time.Second

// NewRecording normalises obs, observed running the traffic in traffic. It
// fails on an Attempt it can't place: one whose webhook-id is no Message's
// ID, or that has no Delivery.
func NewRecording(traffic TrafficFile, obs Observations) (Recording, error) {
	rec := Recording{Traffic: traffic}
	messages := map[string]Message{}
	faults := map[string]Fault{}
	sentAt := map[string]time.Time{}
	// names maps each Message ID to the name of the Message that created it.
	names := map[string]string{}
	// faultRecords maps each Message sent under a fault to the index of its
	// FaultRecord.
	faultRecords := map[string]int{}
	for _, phase := range obs.Scenario.Phases {
		if phase.Fault != "" {
			rec.Faults = append(rec.Faults, FaultRecord{Fault: phase.Fault})
		}
		for _, m := range phase.Messages {
			messages[m.Name], faults[m.Name] = m, phase.Fault
			if phase.Fault != "" {
				faultRecords[m.Name] = len(rec.Faults) - 1
			}
			sent, ok := obs.Sent[m.Name]
			if !ok {
				return Recording{}, fmt.Errorf("message %s was not sent", m.Name)
			}
			sentAt[m.Name] = sent.At
			id := sent.ID.String()
			outcome := "accepted"
			switch {
			case sent.Status != 0:
				outcome = fmt.Sprintf("refused: %d", sent.Status)
			case names[id] != "":
				outcome = "same as " + names[id]
			default:
				names[id] = m.Name
			}
			rec.Messages = append(rec.Messages, MessageRecord{Name: m.Name, Outcome: outcome})
		}
	}
	behaviours := map[string]Behaviour{}
	for _, app := range obs.Scenario.Applications {
		for _, ep := range app.Endpoints {
			behaviours[ep.Name] = ep.Behaviour
		}
	}

	type key struct{ message, endpoint string }
	records := map[key]*DeliveryRecord{}
	// byEndpoint lists each Endpoint's Deliveries, to place its TLS
	// handshakes, which carry no webhook-id.
	byEndpoint := map[string][]key{}
	// repeats counts, for each Delivery, the Attempts its Endpoint received
	// beyond those Herald recorded. A dispatcher that can't record an
	// Attempt it made, as when Postgres is slowed, makes it again:
	// at-least-once delivery. Whether it does depends on timing, so a repeat
	// of the Attempt before it is left out, as many times as there are
	// repeats.
	repeats := map[key]int{}
	for _, d := range obs.Deliveries {
		message, ok := names[d.MessageID.String()]
		if !ok {
			return Recording{}, fmt.Errorf("delivery %s: message %s is none the scenario sent", d.ID, d.MessageID)
		}
		endpoint, ok := obs.EndpointIDs[d.EndpointID]
		if !ok {
			return Recording{}, fmt.Errorf("delivery %s: endpoint %s is none the scenario created", d.ID, d.EndpointID)
		}
		k := key{message, endpoint}
		records[k] = &DeliveryRecord{
			Message:   message,
			Endpoint:  endpoint,
			Behaviour: behaviours[endpoint],
			Fault:     faults[message],
			State:     d.State,
		}
		byEndpoint[endpoint] = append(byEndpoint[endpoint], k)
		repeats[k] = -obs.Recorded[d.ID]
	}

	attempts := slices.Clone(obs.Attempts)
	slices.SortStableFunc(attempts, func(a, b endpointtest.Attempt) int { return a.ReceivedAt.Compare(b.ReceivedAt) })
	keys := make([]key, len(attempts))
	for i, a := range attempts {
		endpoint, ok := obs.EndpointURLs[a.EndpointURL]
		if !ok {
			return Recording{}, fmt.Errorf("attempt to %s, which the scenario didn't create", a.EndpointURL)
		}
		var k key
		if a.Handshake {
			ks := byEndpoint[endpoint]
			if len(ks) != 1 {
				return Recording{}, fmt.Errorf("TLS handshake to %s, which has %d Deliveries: a handshake can be placed only with one", endpoint, len(ks))
			}
			k = ks[0]
		} else {
			message, ok := names[a.WebhookID]
			if !ok {
				return Recording{}, fmt.Errorf("attempt to %s with webhook-id %q, which is no Message's ID", endpoint, a.WebhookID)
			}
			k = key{message, endpoint}
		}
		if _, ok := records[k]; !ok {
			return Recording{}, fmt.Errorf("attempt for %s to %s, which has no Delivery", k.message, k.endpoint)
		}
		keys[i] = k
		repeats[k]++
	}

	firsts := map[key]time.Time{}
	for i, a := range attempts {
		k := keys[i]
		r := records[k]
		ar := AttemptRecord{Handshake: a.Handshake, Answer: a.Answer.String()}
		first, ok := firsts[k]
		if !ok {
			first, firsts[k] = a.ReceivedAt, a.ReceivedAt
		}
		switch {
		case r.Fault == "":
			ms := a.ReceivedAt.Sub(first).Milliseconds()
			ar.AfterMs = &ms
		case len(r.Attempts) == 0:
			// The Delivery's own Message is the one that created it.
			if a.ReceivedAt.Sub(sentAt[k.message]) >= lateAfter {
				rec.Faults[faultRecords[k.message]].Late = true
			}
		}
		if a.Handshake {
			ar.Answer = "untrusted certificate"
		} else {
			ar.Header = normaliseHeader(a.Header, names)
			ar.Body = describeBody(a.Body, messages[k.message])
			ar.Signature = "verified"
			if a.SignatureErr != nil {
				ar.Signature = a.SignatureErr.Error()
			}
		}
		if n := len(r.Attempts); repeats[k] > 0 && n > 0 && sameAttempt(r.Attempts[n-1], ar) {
			repeats[k]--
			continue
		}
		r.Attempts = append(r.Attempts, ar)
	}

	for _, r := range records {
		rec.Deliveries = append(rec.Deliveries, *r)
	}
	slices.SortFunc(rec.Deliveries, func(a, b DeliveryRecord) int {
		return cmp.Or(strings.Compare(a.Message, b.Message), strings.Compare(a.Endpoint, b.Endpoint))
	})
	return rec, nil
}

// sameAttempt reports whether a and b record the same Attempt, timing
// aside.
func sameAttempt(a, b AttemptRecord) bool {
	return a.Handshake == b.Handshake && maps.Equal(a.Header, b.Header) &&
		a.Body == b.Body && a.Signature == b.Signature && a.Answer == b.Answer
}

// normaliseHeader returns h with each header's values joined, the
// webhook-id as the name of its Message, and the timestamp and signature,
// which vary from run to run, replaced.
func normaliseHeader(h http.Header, names map[string]string) map[string]string {
	out := map[string]string{}
	for name, values := range h {
		v := strings.Join(values, ", ")
		switch name {
		case "Webhook-Id":
			if n, ok := names[v]; ok {
				v = n
			}
		case "Webhook-Timestamp":
			v = "<timestamp>"
		case "Webhook-Signature":
			v = "<signature>"
		}
		out[name] = v
	}
	return out
}

// describeBody returns "the payload of <name>" if body is m's payload, and
// otherwise its size and SHA-256.
func describeBody(body []byte, m Message) string {
	if string(body) == m.Payload {
		return "the payload of " + m.Name
	}
	return fmt.Sprintf("%d bytes, sha256 %x", len(body), sha256.Sum256(body))
}
