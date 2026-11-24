package scenario_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/scenario"
)

// The run edgeRun describes: app-1 has ep-1, which accepts every Event type
// and succeeds, and ep-2, which accepts refund Event types and fails twice.
// m001 (invoice.paid) reaches ep-1; m002 (refund.created) reaches both.
const (
	ep1URL = "http://host.docker.internal:50001/endpoints/0"
	ep2URL = "http://host.docker.internal:50001/endpoints/1"
)

var (
	edgeM1    = uuid.MustParse("0198a1b2-0000-7000-8000-000000000001")
	edgeM2    = uuid.MustParse("0198a1b2-0000-7000-8000-000000000002")
	edgeEP1   = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000e1")
	edgeEP2   = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000e2")
	edgeD11   = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d1")
	edgeD21   = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d2")
	edgeD22   = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d3")
	edgeStart = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
)

// edgeRun returns the Observations of a run that keeps every rule at the
// edge. Each call returns a fresh copy for a test to break.
func edgeRun() scenario.Observations {
	m1 := scenario.Message{Name: "m001", Application: "app-1", EventType: "invoice.paid", Payload: `{"message":"m001","data":"abc"}`}
	m2 := scenario.Message{Name: "m002", Application: "app-1", EventType: "refund.created", Payload: `{"message":"m002","data":"def"}`}
	at := func(ms int) time.Time { return edgeStart.Add(time.Duration(ms) * time.Millisecond) }
	attempt := func(url string, n int, id uuid.UUID, body string, received, answered, status int) endpointtest.Attempt {
		return endpointtest.Attempt{
			EndpointURL:    url,
			DeliveryNumber: n,
			WebhookID:      id.String(),
			Header:         http.Header{"Webhook-Id": {id.String()}},
			Body:           []byte(body),
			ReceivedAt:     at(received),
			AnsweredAt:     at(answered),
			Answer:         endpointtest.Status(status),
		}
	}
	return scenario.Observations{
		Scenario: scenario.Scenario{
			Applications: []scenario.Application{{Name: "app-1", Endpoints: []scenario.Endpoint{
				{Name: "app-1/ep-1", Behaviour: scenario.Succeed},
				{Name: "app-1/ep-2", Filter: []string{"refund.*"}, Behaviour: scenario.Fail},
			}}},
			Phases: []scenario.Phase{{Messages: []scenario.Message{m1, m2}}},
		},
		Sent:         map[string]scenario.Sent{"m001": {ID: edgeM1}, "m002": {ID: edgeM2}},
		EndpointURLs: map[string]string{ep1URL: "app-1/ep-1", ep2URL: "app-1/ep-2"},
		EndpointIDs:  map[uuid.UUID]string{edgeEP1: "app-1/ep-1", edgeEP2: "app-1/ep-2"},
		Deliveries: []e2e.Delivery{
			{ID: edgeD11, MessageID: edgeM1, EndpointID: edgeEP1, State: e2e.StateSucceeded},
			{ID: edgeD21, MessageID: edgeM2, EndpointID: edgeEP1, State: e2e.StateSucceeded},
			{ID: edgeD22, MessageID: edgeM2, EndpointID: edgeEP2, State: e2e.StateSucceeded},
		},
		Recorded: map[uuid.UUID]int{edgeD11: 1, edgeD21: 1, edgeD22: 3},
		Attempts: []endpointtest.Attempt{
			attempt(ep1URL, 1, edgeM1, m1.Payload, 0, 5, 200),
			attempt(ep1URL, 1, edgeM2, m2.Payload, 100, 105, 200),
			// The retry schedule's second and third delays at the local
			// stack's scale are 5 and 60 ms, less 10% jitter at most.
			attempt(ep2URL, 1, edgeM2, m2.Payload, 100, 105, 500),
			attempt(ep2URL, 2, edgeM2, m2.Payload, 600, 605, 500),
			attempt(ep2URL, 3, edgeM2, m2.Payload, 1200, 1205, 200),
		},
	}
}

func TestCheckEdgePassesARunThatKeepsEveryRule(t *testing.T) {
	t.Parallel()
	if got := scenario.CheckEdge(edgeRun()); len(got) != 0 {
		t.Errorf("CheckEdge = %v, want no violations", got)
	}
}

// TestCheckEdgeNamesWhatBroke breaks one rule at a time and checks that
// CheckEdge reports that rule, naming the Delivery or Endpoint that broke
// it, and no other.
func TestCheckEdgeNamesWhatBroke(t *testing.T) {
	t.Parallel()
	// attemptTo returns the nth Attempt that url received.
	attemptTo := func(obs *scenario.Observations, url string, n int) *endpointtest.Attempt {
		for i := range obs.Attempts {
			if obs.Attempts[i].EndpointURL == url {
				if n--; n == 0 {
					return &obs.Attempts[i]
				}
			}
		}
		panic("no such Attempt")
	}
	at := func(ms int) time.Time { return edgeStart.Add(time.Duration(ms) * time.Millisecond) }
	tests := []struct {
		name    string
		breakIt func(obs *scenario.Observations)
		want    []violation
	}{
		{
			// Reported once for the Endpoint, however many handshakes.
			name: "TLS handshakes at an Endpoint with two Deliveries",
			breakIt: func(obs *scenario.Observations) {
				for range 2 {
					obs.Attempts = append(obs.Attempts, endpointtest.Attempt{EndpointURL: ep1URL, Handshake: true, ReceivedAt: at(50), AnsweredAt: at(50)})
				}
			},
			want: []violation{{"fan-out", "app-1/ep-1"}},
		},
		{
			name: "a signature that doesn't verify",
			breakIt: func(obs *scenario.Observations) {
				attemptTo(obs, ep2URL, 2).SignatureErr = errors.New("no matching signature")
			},
			want: []violation{{"signature", "m002 to app-1/ep-2"}},
		},
		{
			name: "a webhook-id that changes between Attempts",
			breakIt: func(obs *scenario.Observations) {
				a := attemptTo(obs, ep2URL, 2)
				a.WebhookID = edgeM1.String()
			},
			want: []violation{{"webhook-id", "m002 to app-1/ep-2"}},
		},
		{
			name: "two Messages with one webhook-id at an Endpoint",
			breakIt: func(obs *scenario.Observations) {
				for _, n := range []int{1, 2, 3} {
					attemptTo(obs, ep2URL, n).WebhookID = edgeM1.String()
				}
				attemptTo(obs, ep1URL, 2).WebhookID = edgeM1.String()
			},
			want: []violation{
				{"webhook-id", "app-1/ep-1"},
				{"webhook-id", "m002 to app-1/ep-1"},
				{"webhook-id", "m002 to app-1/ep-2"},
			},
		},
		{
			name: "a changed body byte",
			breakIt: func(obs *scenario.Observations) {
				attemptTo(obs, ep1URL, 1).Body = []byte(`{"message":"m001","data":"abd"}`)
			},
			want: []violation{{"payload", "m001 to app-1/ep-1"}},
		},
		{
			name: "a Message that misses a matching Endpoint",
			breakIt: func(obs *scenario.Observations) {
				obs.Attempts = slices.DeleteFunc(obs.Attempts, func(a endpointtest.Attempt) bool {
					return a.EndpointURL == ep1URL && a.WebhookID == edgeM2.String()
				})
			},
			want: []violation{{"fan-out", "m002 to app-1/ep-1"}},
		},
		{
			name: "a Message at an Endpoint whose filter doesn't match",
			breakIt: func(obs *scenario.Observations) {
				a := *attemptTo(obs, ep1URL, 1)
				a.EndpointURL = ep2URL
				a.ReceivedAt, a.AnsweredAt = at(2000), at(2005)
				obs.Attempts = append(obs.Attempts, a)
			},
			want: []violation{{"fan-out", "m001 to app-1/ep-2"}},
		},
		{
			name: "a fifth Attempt in flight at an Endpoint",
			breakIt: func(obs *scenario.Observations) {
				// Five Attempts of m001 at once, all of which Herald
				// recorded, the first four failing.
				obs.Recorded[edgeD11] = 5
				first := attemptTo(obs, ep1URL, 1)
				first.Answer = endpointtest.Status(500)
				first.AnsweredAt = at(5000)
				for i := range 4 {
					a := *first
					a.ReceivedAt = at(1 + i)
					a.AnsweredAt = at(5000)
					if i == 3 {
						a.Answer = endpointtest.Status(200)
					}
					obs.Attempts = append(obs.Attempts, a)
				}
			},
			want: []violation{{"in flight", "app-1/ep-1"}, {"retry schedule", "m001 to app-1/ep-1"}},
		},
		{
			name: "an Attempt after a 2xx that Herald recorded",
			breakIt: func(obs *scenario.Observations) {
				obs.Recorded[edgeD11] = 2
				a := *attemptTo(obs, ep1URL, 1)
				a.ReceivedAt, a.AnsweredAt = at(3000), at(3005)
				obs.Attempts = append(obs.Attempts, a)
			},
			want: []violation{{"after success", "m001 to app-1/ep-1"}},
		},
		{
			name: "more than 8 Attempts recorded",
			breakIt: func(obs *scenario.Observations) {
				obs.Recorded[edgeD22] = 9
			},
			want: []violation{{"attempts", "m002 to app-1/ep-2"}},
		},
		{
			name: "an Attempt before the retry schedule allows",
			breakIt: func(obs *scenario.Observations) {
				a := attemptTo(obs, ep2URL, 3)
				// The third delay is 60 ms at least 54 ms; this comes 40 ms
				// after the second.
				a.ReceivedAt, a.AnsweredAt = at(640), at(645)
			},
			want: []violation{{"retry schedule", "m002 to app-1/ep-2"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs := edgeRun()
			tt.breakIt(&obs)
			got := violations(scenario.CheckEdge(obs))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("violations (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCheckEdgeExcusesRepeatsHeraldDidntRecord checks that an Attempt Herald
// made again, because it couldn't record it or shutdown cut it off, may
// follow a 2xx or come early: at-least-once delivery (ADR 0001). As many may
// as the Endpoint received beyond what Herald recorded, and no more.
func TestCheckEdgeExcusesRepeatsHeraldDidntRecord(t *testing.T) {
	t.Parallel()
	at := func(ms int) time.Time { return edgeStart.Add(time.Duration(ms) * time.Millisecond) }
	tests := []struct {
		name string
		// extra are Attempts of m002 at ep-2 added to the run, each received
		// at the given time with the given status. Herald records none.
		extra []int
		// status is the status of each extra Attempt.
		status int
		want   []violation
	}{
		{name: "a 2xx Attempt made again", extra: []int{1500}, status: 200},
		{name: "a cut-off Attempt made again at once", extra: []int{601}, status: 500},
		{name: "two repeats", extra: []int{601, 1201}, status: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs := edgeRun()
			body := `{"message":"m002","data":"def"}`
			for _, ms := range tt.extra {
				obs.Attempts = append(obs.Attempts, endpointtest.Attempt{
					EndpointURL: ep2URL,
					WebhookID:   edgeM2.String(),
					Header:      http.Header{"Webhook-Id": {edgeM2.String()}},
					Body:        []byte(body),
					ReceivedAt:  at(ms),
					AnsweredAt:  at(ms + 5),
					Answer:      endpointtest.Status(tt.status),
				})
			}
			if got := violations(scenario.CheckEdge(obs)); len(got) != 0 {
				t.Errorf("CheckEdge = %v, want no violations", got)
			}

			// One Attempt more than the Endpoint received beyond the
			// recorded ones is no longer excused: Herald recorded it.
			obs.Recorded[edgeD22] += len(tt.extra)
			if got := violations(scenario.CheckEdge(obs)); len(got) == 0 {
				t.Error("with every Attempt recorded, CheckEdge = no violations, want some")
			}
		})
	}
}

// TestCheckEdgeFailsOnNothingToCheck checks that every rule fails on a run
// whose Endpoints received nothing and that has no Deliveries, rather than
// passing on nothing.
func TestCheckEdgeFailsOnNothingToCheck(t *testing.T) {
	t.Parallel()
	obs := edgeRun()
	obs.Attempts, obs.Deliveries, obs.Recorded = nil, nil, nil
	got := map[string]bool{}
	for _, v := range scenario.CheckEdge(obs) {
		got[v.Rule] = true
	}
	for _, rule := range []string{"signature", "webhook-id", "payload", "fan-out", "in flight", "after success", "attempts", "retry schedule"} {
		if !got[rule] {
			t.Errorf("no violation of %q on a run with no Attempts", rule)
		}
	}
}

// violation is the part of a scenario.Violation a test checks: which rule
// broke, and where.
type violation struct{ Rule, Subject string }

func violations(vs []scenario.Violation) []violation {
	var out []violation
	for _, v := range vs {
		out = append(out, violation{v.Rule, v.Subject})
	}
	return out
}
