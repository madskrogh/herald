package scenario_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/scenario"
)

// faultRun returns spanRun's Observations and spans with m002 sent in a
// phase of its own, under a dispatcher restart that cut off an Attempt of
// it, which was made again.
func faultRun() (scenario.Observations, []e2e.Span) {
	obs, spans := spanRun()
	underFault(&obs, scenario.DispatcherRestarted)
	i := slices.IndexFunc(spans, func(s e2e.Span) bool { return s.SpanID == "a3" })
	cut := spans[i]
	cut.SpanID = "a3-cut"
	cut.StatusCode = "Error"
	cut.Attributes = with(cut.Attributes, map[string]string{
		"herald.outcome": "cut_off", "herald.status_code": "0", "herald.error": "shutdown",
	})
	return obs, append(spans, cut)
}

// underFault moves m002 out of obs's first phase into a phase of its own,
// under fault.
func underFault(obs *scenario.Observations, fault scenario.Fault) {
	phase := &obs.Scenario.Phases[0]
	m2 := phase.Messages[1]
	phase.Messages = append(phase.Messages[:1], phase.Messages[2:]...)
	obs.Scenario.Phases = append(obs.Scenario.Phases, scenario.Phase{
		Fault: fault, Messages: []scenario.Message{m2},
	})
}

// with returns a copy of m with set's keys set.
func with(m, set map[string]string) map[string]string {
	out := maps.Clone(m)
	maps.Copy(out, set)
	return out
}

func TestNewStatisticsCountsSpansByNameAndOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func() (scenario.Observations, []e2e.Span)
		want map[string]int
	}{
		{
			name: "no fault",
			run:  spanRun,
			want: map[string]int{
				"tenant_api.call /herald.tenant.v1.MessageService/CreateMessage OK": 3,
				"handoff handed_off":      2,
				"store_deliveries stored": 2,
				"attempt succeeded":       3,
				"attempt pending":         2,
			},
		},
		{
			// A fault's spans depend on when it takes hold, so they aren't
			// counted; the Batch, which holds Messages as they come, isn't
			// either.
			name: "a Message under a fault",
			run:  faultRun,
			want: map[string]int{
				"tenant_api.call /herald.tenant.v1.MessageService/CreateMessage OK": 3,
				"handoff handed_off":      1,
				"store_deliveries stored": 1,
				"attempt succeeded":       1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs, spans := tt.run()
			got := scenario.NewStatistics(obs, spans).Counts
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NewStatistics(...).Counts differs (-want +got):\n%s", diff)
			}
		})
	}
}

// timedRun returns spanRun's Observations and spans with a latency and a
// lateness of its own for each Attempt: 10 to 50 ms of latency, and 100,
// 200 and 300 ms of lateness for the first Attempts, which no statistic
// reads, and 1000 and 2000 ms for the retries. m001 is handed off 390 ms
// after its call ended, and m002 290 ms.
func timedRun() (scenario.Observations, []e2e.Span) {
	obs, spans := spanRun()
	timing := map[string][2]string{
		"a1": {"10", "100"}, "a2": {"20", "200"}, "a3": {"30", "300"},
		"a4": {"40", "1000"}, "a5": {"50", "2000"},
	}
	for i, s := range spans {
		if t, ok := timing[s.SpanID]; ok {
			spans[i].Attributes = with(s.Attributes, map[string]string{
				"herald.latency_ms": t[0], "herald.lateness_ms": t[1],
			})
		}
	}
	return obs, spans
}

func TestNewStatisticsTimesHandoffAndAttempts(t *testing.T) {
	t.Parallel()
	slow := func() (scenario.Observations, []e2e.Span) {
		obs, spans := timedRun()
		obs.Scenario.Applications[0].Endpoints[1].Behaviour = scenario.Slow
		return obs, spans
	}
	faulty := func() (scenario.Observations, []e2e.Span) {
		obs, spans := timedRun()
		underFault(&obs, scenario.PostgresSlowed)
		return obs, spans
	}
	tests := []struct {
		name string
		run  func() (scenario.Observations, []e2e.Span)
		want map[string]scenario.Distribution
	}{
		{
			// Nearest rank: the 50th percentile of five is the 3rd, and the
			// 95th the 5th; of two, the 1st and the 2nd.
			name: "no fault",
			run:  timedRun,
			want: map[string]scenario.Distribution{
				"handoff lag":     {N: 2, P50Ms: 290, P95Ms: 390},
				"Attempt latency": {N: 5, P50Ms: 30, P95Ms: 50},
				"retry lateness":  {N: 2, P50Ms: 1000, P95Ms: 2000},
			},
		},
		{
			// A slow Endpoint's latency is the harness's, not Herald's.
			name: "a slow Endpoint",
			run:  slow,
			want: map[string]scenario.Distribution{
				"handoff lag":     {N: 2, P50Ms: 290, P95Ms: 390},
				"Attempt latency": {N: 2, P50Ms: 10, P95Ms: 20},
				"retry lateness":  {N: 2, P50Ms: 1000, P95Ms: 2000},
			},
		},
		{
			// Timing under a fault depends on the machine.
			name: "a Message under a fault",
			run:  faulty,
			want: map[string]scenario.Distribution{
				"handoff lag":     {N: 1, P50Ms: 390, P95Ms: 390},
				"Attempt latency": {N: 1, P50Ms: 10, P95Ms: 10},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			obs, spans := tt.run()
			got := scenario.NewStatistics(obs, spans).Timing
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("NewStatistics(...).Timing differs (-want +got):\n%s", diff)
			}
		})
	}
}
