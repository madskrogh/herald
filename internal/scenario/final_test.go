package scenario_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/scenario"
)

// finalRun returns edgeRun with a third Message, m003, sent again with
// m001's Idempotency-Key, and the final state that run leaves in Postgres.
func finalRun() (scenario.Observations, scenario.FinalState) {
	obs := edgeRun()
	m1 := &obs.Scenario.Phases[0].Messages[0]
	m1.IdempotencyKey = "key-m001"
	m3 := *m1
	m3.Name = "m003"
	obs.Scenario.Phases[0].Messages = append(obs.Scenario.Phases[0].Messages, m3)
	obs.Sent["m003"] = scenario.Sent{ID: edgeM1}
	state := scenario.FinalState{
		Messages: []e2e.MessageStatus{
			{ID: edgeM1, HandedOff: true},
			{ID: edgeM2, HandedOff: true},
		},
		Deliveries: obs.Deliveries,
	}
	return obs, state
}

func TestCheckFinalStatePassesARunThatKeepsEveryRule(t *testing.T) {
	t.Parallel()
	obs, state := finalRun()
	if got := scenario.CheckFinalState(obs, state); len(got) != 0 {
		t.Errorf("CheckFinalState = %v, want no violations", got)
	}
}

// TestCheckFinalStateNamesWhatBroke breaks one rule at a time and checks
// that CheckFinalState reports that rule, naming the Delivery or Message
// that broke it, and no other.
func TestCheckFinalStateNamesWhatBroke(t *testing.T) {
	t.Parallel()
	extra := uuid.MustParse("0198a1b2-0000-7000-8000-0000000000f1")
	tests := []struct {
		name    string
		breakIt func(obs *scenario.Observations, state *scenario.FinalState)
		want    []violation
	}{
		{
			name: "a Delivery left pending",
			breakIt: func(_ *scenario.Observations, state *scenario.FinalState) {
				state.Deliveries[0].State = e2e.StatePending
			},
			want: []violation{{"final state", "m001 to app-1/ep-1"}},
		},
		{
			name: "succeeded, though its last Attempt failed",
			breakIt: func(obs *scenario.Observations, _ *scenario.FinalState) {
				obs.Attempts[0].Answer = endpointtest.Status(500)
			},
			want: []violation{{"final state", "m001 to app-1/ep-1"}},
		},
		{
			name: "succeeded, though its last Attempt took longer than the timeout",
			breakIt: func(obs *scenario.Observations, _ *scenario.FinalState) {
				obs.Attempts[0].AnsweredAt = obs.Attempts[0].ReceivedAt.Add(16 * time.Second)
			},
			want: []violation{{"final state", "m001 to app-1/ep-1"}},
		},
		{
			name: "failed, though its last Attempt got a 2xx",
			breakIt: func(_ *scenario.Observations, state *scenario.FinalState) {
				state.Deliveries[2].State = e2e.StateFailed
			},
			want: []violation{{"final state", "m002 to app-1/ep-2"}},
		},
		{
			name: "a Message left in the Outbox",
			breakIt: func(_ *scenario.Observations, state *scenario.FinalState) {
				state.Messages[1].HandedOff = false
			},
			want: []violation{{"outbox", "m002"}},
		},
		{
			name: "a Message parked",
			breakIt: func(_ *scenario.Observations, state *scenario.FinalState) {
				state.Messages[1].HandedOff, state.Messages[1].Parked = false, true
			},
			want: []violation{{"outbox", "m002"}},
		},
		{
			name: "a repeated Idempotency-Key that created a second Message",
			breakIt: func(obs *scenario.Observations, state *scenario.FinalState) {
				obs.Sent["m003"] = scenario.Sent{ID: extra}
				state.Messages = append(state.Messages, e2e.MessageStatus{ID: extra, HandedOff: true})
			},
			want: []violation{{"duplicates", "m003"}},
		},
		{
			name: "a Message with two Deliveries to one Endpoint",
			breakIt: func(_ *scenario.Observations, state *scenario.FinalState) {
				d := state.Deliveries[0]
				d.ID = extra
				state.Deliveries = append(state.Deliveries, d)
			},
			want: []violation{{"duplicates", "m001 to app-1/ep-1"}},
		},
		{
			name: "a Message with no Delivery to a matching Endpoint",
			breakIt: func(_ *scenario.Observations, state *scenario.FinalState) {
				state.Deliveries = state.Deliveries[:2]
			},
			want: []violation{{"duplicates", "m002 to app-1/ep-2"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs, state := finalRun()
			tt.breakIt(&obs, &state)
			got := violations(scenario.CheckFinalState(obs, state))
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("violations (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCheckFinalStateFailsOnNothingToCheck checks that every rule fails on
// a run that left nothing in Postgres, rather than passing on nothing.
func TestCheckFinalStateFailsOnNothingToCheck(t *testing.T) {
	t.Parallel()
	obs, _ := finalRun()
	got := map[string]bool{}
	for _, v := range scenario.CheckFinalState(obs, scenario.FinalState{}) {
		got[v.Rule] = true
	}
	for _, rule := range []string{"final state", "outbox", "duplicates"} {
		if !got[rule] {
			t.Errorf("no violation of %q on a run that left nothing in Postgres", rule)
		}
	}
}

// TestCheckFinalStateTakesTheFirstAcceptedMessageAsTheOriginal checks that
// a refused Message with an Idempotency-Key isn't the Message a later one
// with the key returns.
func TestCheckFinalStateTakesTheFirstAcceptedMessageAsTheOriginal(t *testing.T) {
	t.Parallel()
	obs, state := finalRun()
	m0 := obs.Scenario.Phases[0].Messages[0]
	m0.Name = "m000"
	obs.Scenario.Phases[0].Messages = append([]scenario.Message{m0}, obs.Scenario.Phases[0].Messages...)
	obs.Sent["m000"] = scenario.Sent{Status: 400}
	if got := scenario.CheckFinalState(obs, state); len(got) != 0 {
		t.Errorf("CheckFinalState = %v, want no violations", got)
	}
}
