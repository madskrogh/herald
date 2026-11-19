package scenario_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

func TestCompare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// change changes a copy of the baseline into the recording compared
		// with it.
		change func(r *scenario.Recording)
		want   []scenario.Difference
	}{
		{
			name:   "the same",
			change: func(*scenario.Recording) {},
		},
		{
			name: "retry later within the margin",
			// 25% of 3600 ms plus a second.
			change: func(r *scenario.Recording) { *r.Deliveries[0].Attempts[2].AfterMs = 3600 + 1900 },
		},
		{
			name:   "retry earlier within the margin",
			change: func(r *scenario.Recording) { *r.Deliveries[0].Attempts[2].AfterMs = 3600 - 1900 },
		},
		{
			name:   "retry later beyond the margin",
			change: func(r *scenario.Recording) { *r.Deliveries[0].Attempts[2].AfterMs = 7200 },
			want: []scenario.Difference{
				{Delivery: "m001 to app-1/ep-1", Field: "attempt 3: after_ms", Want: "3600", Got: "7200"},
			},
		},
		{
			name:   "retry earlier beyond the margin",
			change: func(r *scenario.Recording) { *r.Deliveries[0].Attempts[2].AfterMs = 3600 - 1901 },
			want: []scenario.Difference{
				{Delivery: "m001 to app-1/ep-1", Field: "attempt 3: after_ms", Want: "3600", Got: "1699"},
			},
		},
		{
			name:   "dropped header",
			change: func(r *scenario.Recording) { delete(r.Deliveries[1].Attempts[0].Header, "User-Agent") },
			want: []scenario.Difference{
				{Delivery: "m002 to app-1/ep-1", Field: "attempt 1: header User-Agent", Want: "Herald", Got: "(none)"},
			},
		},
		{
			name:   "added header",
			change: func(r *scenario.Recording) { r.Deliveries[1].Attempts[0].Header["X-Herald"] = "1" },
			want: []scenario.Difference{
				{Delivery: "m002 to app-1/ep-1", Field: "attempt 1: header X-Herald", Want: "(none)", Got: "1"},
			},
		},
		{
			name:   "changed body",
			change: func(r *scenario.Recording) { r.Deliveries[0].Attempts[1].Body = "3 bytes, sha256 abc" },
			want: []scenario.Difference{
				{Delivery: "m001 to app-1/ep-1", Field: "attempt 2: body", Want: "the payload of m001", Got: "3 bytes, sha256 abc"},
			},
		},
		{
			name: "signature, answer and state",
			change: func(r *scenario.Recording) {
				r.Deliveries[1].Attempts[0].Signature = "no matching signature"
				r.Deliveries[1].Attempts[0].Answer = "500"
				r.Deliveries[1].State = "failed"
			},
			want: []scenario.Difference{
				{Delivery: "m002 to app-1/ep-1", Field: "state", Want: "succeeded", Got: "failed"},
				{Delivery: "m002 to app-1/ep-1", Field: "attempt 1: signature", Want: "verified", Got: "no matching signature"},
				{Delivery: "m002 to app-1/ep-1", Field: "attempt 1: answer", Want: "200", Got: "500"},
			},
		},
		{
			name:   "fault without effect",
			change: func(r *scenario.Recording) { r.Faults[0].Late = false },
			want: []scenario.Difference{
				{Delivery: "postgres slowed", Field: "late", Want: "true", Got: "false"},
			},
		},
		{
			name:   "fewer Attempts",
			change: func(r *scenario.Recording) { r.Deliveries[0].Attempts = r.Deliveries[0].Attempts[:2] },
			want: []scenario.Difference{
				{Delivery: "m001 to app-1/ep-1", Field: "attempts", Want: "3", Got: "2"},
			},
		},
		{
			name: "Delivery missing and another added",
			change: func(r *scenario.Recording) {
				r.Deliveries[1].Endpoint = "app-1/ep-2"
			},
			want: []scenario.Difference{
				{Delivery: "m002 to app-1/ep-1", Field: "delivery", Want: "present", Got: "(none)"},
				{Delivery: "m002 to app-1/ep-2", Field: "delivery", Want: "(none)", Got: "present"},
			},
		},
		{
			name:   "Message outcome",
			change: func(r *scenario.Recording) { r.Messages[1].Outcome = "accepted" },
			want: []scenario.Difference{
				{Delivery: "m003", Field: "outcome", Want: "same as m001", Got: "accepted"},
			},
		},
		{
			name:   "seed",
			change: func(r *scenario.Recording) { r.Seed = 2 },
			want: []scenario.Difference{
				{Field: "seed", Want: "1", Got: "2"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := baseline()
			tt.change(&got)
			if diff := cmp.Diff(tt.want, scenario.Compare(baseline(), got)); diff != "" {
				t.Errorf("Compare (-want +got):\n%s", diff)
			}
		})
	}
}

// baseline returns a Recording to compare with, a new one on every call.
func baseline() scenario.Recording {
	ms := func(n int64) *int64 { return &n }
	header := func(message string) map[string]string {
		return map[string]string{"Content-Type": "application/json", "User-Agent": "Herald", "Webhook-Id": message}
	}
	return scenario.Recording{
		Seed:   1,
		Faults: []scenario.FaultRecord{{Fault: scenario.PostgresSlowed, Late: true}},
		Messages: []scenario.MessageRecord{
			{Name: "m001", Outcome: "accepted"},
			{Name: "m003", Outcome: "same as m001"},
		},
		Deliveries: []scenario.DeliveryRecord{
			{
				Message: "m001", Endpoint: "app-1/ep-1", Behaviour: scenario.Fail, State: "succeeded",
				Attempts: []scenario.AttemptRecord{
					{AfterMs: ms(0), Header: header("m001"), Body: "the payload of m001", Signature: "verified", Answer: "500"},
					{AfterMs: ms(600), Header: header("m001"), Body: "the payload of m001", Signature: "verified", Answer: "500"},
					{AfterMs: ms(3600), Header: header("m001"), Body: "the payload of m001", Signature: "verified", Answer: "200"},
				},
			},
			{
				Message: "m002", Endpoint: "app-1/ep-1", Behaviour: scenario.Fail, State: "succeeded",
				Attempts: []scenario.AttemptRecord{
					{AfterMs: ms(0), Header: header("m002"), Body: "the payload of m002", Signature: "verified", Answer: "200"},
				},
			},
		},
	}
}
