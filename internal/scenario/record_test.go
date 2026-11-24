package scenario_test

import (
	"errors"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/scenario"
)

// TestNewRecordingNormalisesARun checks a worked example: what varies from
// run to run (IDs, timestamps, signatures, timing under a fault) is replaced,
// and everything else is kept as received.
func TestNewRecordingNormalisesARun(t *testing.T) {
	t.Parallel()
	const (
		failURL = "http://host.docker.internal:50001/endpoints/0"
		tlsURL  = "https://host.docker.internal:50002/"
	)
	var (
		id1    = uuid.MustParse("0198a1b2-0000-7000-8000-000000000001")
		id4    = uuid.MustParse("0198a1b2-0000-7000-8000-000000000004")
		id5    = uuid.MustParse("0198a1b2-0000-7000-8000-000000000005")
		id6    = uuid.MustParse("0198a1b2-0000-7000-8000-000000000006")
		failID = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000e1")
		tlsID  = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000e2")
		d1     = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d1")
		d4     = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d4")
		d5     = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d5")
		d6     = uuid.MustParse("0198a1b2-0000-7000-8000-0000000000d6")
		t0     = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	)
	m1 := scenario.Message{Name: "m001", Application: "app-1", EventType: "invoice.paid", Payload: `{"message":"m001"}`, IdempotencyKey: "key-m001"}
	m2 := m1
	m2.Name = "m002"
	m3 := scenario.Message{Name: "m003", Application: "app-1", EventType: "invoice.paid", Payload: `{"message":"m003"}`, IdempotencyKey: "key-m001"}
	m4 := scenario.Message{Name: "m004", Application: "app-2", EventType: "payout.sent", Payload: `{"message":"m004"}`}
	m5 := scenario.Message{Name: "m005", Application: "app-1", EventType: "invoice.paid", Payload: `{"message":"m005"}`}
	m6 := scenario.Message{Name: "m006", Application: "app-1", EventType: "invoice.paid", Payload: `{"message":"m006"}`}
	run := scenario.Observations{
		Scenario: scenario.Scenario{
			Seed: 42,
			Applications: []scenario.Application{
				{Name: "app-1", Endpoints: []scenario.Endpoint{{Name: "app-1/ep-1", Behaviour: scenario.Fail}}},
				{Name: "app-2", Endpoints: []scenario.Endpoint{{Name: "app-2/ep-1", Behaviour: scenario.TLSError}}},
			},
			Phases: []scenario.Phase{
				{Messages: []scenario.Message{m1, m2, m3, m4}},
				{Fault: scenario.NATSPaused, Messages: []scenario.Message{m5, m6}},
			},
		},
		Sent: map[string]scenario.Sent{
			"m001": {ID: id1},
			"m002": {ID: id1},
			"m003": {Status: http.StatusUnprocessableEntity},
			"m004": {ID: id4},
			"m005": {ID: id5, At: t0.Add(16 * time.Second)},
			"m006": {ID: id6, At: t0.Add(17 * time.Second)},
		},
		EndpointURLs: map[string]string{failURL: "app-1/ep-1", tlsURL: "app-2/ep-1"},
		EndpointIDs:  map[uuid.UUID]string{failID: "app-1/ep-1", tlsID: "app-2/ep-1"},
		Deliveries: []e2e.Delivery{
			{ID: d5, MessageID: id5, EndpointID: failID, State: e2e.StateSucceeded},
			{ID: d1, MessageID: id1, EndpointID: failID, State: e2e.StateSucceeded},
			{ID: d4, MessageID: id4, EndpointID: tlsID, State: e2e.StateFailed},
			{ID: d6, MessageID: id6, EndpointID: failID, State: e2e.StateSucceeded},
		},
		// Herald recorded one Attempt fewer to m005 than its Endpoint received.
		Recorded: map[uuid.UUID]int{d1: 2, d4: 2, d5: 2, d6: 3},
		Attempts: []endpointtest.Attempt{
			signed(failURL, id1, t0, m1.Payload, endpointtest.Status(http.StatusInternalServerError)),
			{EndpointURL: tlsURL, ReceivedAt: t0.Add(time.Second), Handshake: true},
			signed(failURL, id1, t0.Add(612*time.Millisecond), m1.Payload, endpointtest.Answer{
				Status: http.StatusFound, Header: http.Header{"Location": {"/moved"}}, Body: []byte("moved"),
			}),
			{EndpointURL: tlsURL, ReceivedAt: t0.Add(3500 * time.Millisecond), Handshake: true},
			signed(failURL, id5, t0.Add(20*time.Second), `{"message":"changed"}`, endpointtest.Answer{Kind: endpointtest.AnswerReset}),
			signed(failURL, id5, t0.Add(25*time.Second), m5.Payload, endpointtest.Answer{Kind: endpointtest.AnswerNone}),
			// The same Attempt again, which Herald didn't record: it is
			// recorded once.
			signed(failURL, id5, t0.Add(30*time.Second), m5.Payload, endpointtest.Answer{Kind: endpointtest.AnswerNone}),
			// Retries answered the same, which Herald recorded: each is kept.
			signed(failURL, id6, t0.Add(31*time.Second), m6.Payload, endpointtest.Status(http.StatusInternalServerError)),
			signed(failURL, id6, t0.Add(32*time.Second), m6.Payload, endpointtest.Status(http.StatusInternalServerError)),
			signed(failURL, id6, t0.Add(33*time.Second), m6.Payload, endpointtest.Status(http.StatusOK)),
		},
	}
	// The second and third Attempts to m005 failed to verify.
	run.Attempts[5].SignatureErr = errors.New("no matching signature")
	run.Attempts[6].SignatureErr = errors.New("no matching signature")

	got, err := scenario.NewRecording(run)
	if err != nil {
		t.Fatalf("NewRecording: %v", err)
	}

	header := func(message string) map[string]string {
		return map[string]string{
			"Content-Type":      "application/json",
			"User-Agent":        "Herald",
			"Webhook-Id":        message,
			"Webhook-Signature": "<signature>",
			"Webhook-Timestamp": "<timestamp>",
		}
	}
	ms := func(n int64) *int64 { return &n }
	want := scenario.Recording{
		Seed:   42,
		Faults: []scenario.FaultRecord{{Fault: scenario.NATSPaused, Late: true}},
		Messages: []scenario.MessageRecord{
			{Name: "m001", Outcome: "accepted"},
			{Name: "m002", Outcome: "same as m001"},
			{Name: "m003", Outcome: "refused: 422"},
			{Name: "m004", Outcome: "accepted"},
			{Name: "m005", Outcome: "accepted"},
			{Name: "m006", Outcome: "accepted"},
		},
		Deliveries: []scenario.DeliveryRecord{
			{
				Message: "m001", Endpoint: "app-1/ep-1", Behaviour: scenario.Fail, State: "succeeded",
				Attempts: []scenario.AttemptRecord{
					{AfterMs: ms(0), Header: header("m001"), Body: "the payload of m001", Signature: "verified", Answer: "500"},
					{AfterMs: ms(612), Header: header("m001"), Body: "the payload of m001", Signature: "verified", Answer: "302, Location: /moved, 5-byte body"},
				},
			},
			{
				Message: "m004", Endpoint: "app-2/ep-1", Behaviour: scenario.TLSError, State: "failed",
				Attempts: []scenario.AttemptRecord{
					{AfterMs: ms(0), Handshake: true, Answer: "untrusted certificate"},
					{AfterMs: ms(2500), Handshake: true, Answer: "untrusted certificate"},
				},
			},
			{
				Message: "m005", Endpoint: "app-1/ep-1", Behaviour: scenario.Fail, Fault: scenario.NATSPaused, State: "succeeded",
				Attempts: []scenario.AttemptRecord{
					{
						Header: header("m005"), Signature: "verified", Answer: "reset",
						Body: "21 bytes, sha256 51945c8dc1c04ec29d2cdd46bdb6cd9894c196b1f1e82882f678ad28ea0933d7",
					},
					{Header: header("m005"), Body: "the payload of m005", Signature: "no matching signature", Answer: "unanswered"},
				},
			},
			{
				Message: "m006", Endpoint: "app-1/ep-1", Behaviour: scenario.Fail, Fault: scenario.NATSPaused, State: "succeeded",
				Attempts: []scenario.AttemptRecord{
					{Header: header("m006"), Body: "the payload of m006", Signature: "verified", Answer: "500"},
					{Header: header("m006"), Body: "the payload of m006", Signature: "verified", Answer: "500"},
					{Header: header("m006"), Body: "the payload of m006", Signature: "verified", Answer: "200"},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NewRecording (-want +got):\n%s", diff)
	}
}

// signed returns an Attempt as the fake Endpoint at url records one from
// Herald for the Message with the given ID, its signature verified.
func signed(url string, messageID uuid.UUID, at time.Time, body string, answer endpointtest.Answer) endpointtest.Attempt {
	return endpointtest.Attempt{
		EndpointURL: url,
		ReceivedAt:  at,
		WebhookID:   messageID.String(),
		Header: http.Header{
			"Content-Type":      {"application/json"},
			"User-Agent":        {"Herald"},
			"Webhook-Id":        {messageID.String()},
			"Webhook-Signature": {"v1,c2lnbmF0dXJl"},
			"Webhook-Timestamp": {"1795003200"},
		},
		Body:   []byte(body),
		Answer: answer,
	}
}
