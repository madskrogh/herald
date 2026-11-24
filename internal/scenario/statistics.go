package scenario

import (
	"math"
	"slices"
	"strconv"

	"github.com/madskrogh/herald/internal/e2e"
)

// Statistics are a run's telemetry statistics, the behaviour comparison's
// second witness (ADR 0008). They are read from the spans of Messages sent
// in phases without a fault: what a fault does depends on when it takes
// hold, and how long it holds Messages back on the machine.
type Statistics struct {
	// Counts holds how many spans of each kind the run emitted, by kind:
	// "<span> <outcome>", or "<span> <method> <outcome>" for a Tenant API
	// call, which is counted in every phase. No Batch is counted, since how
	// Messages are batched depends on when they arrive.
	Counts map[string]int `json:"counts"`
	// Timing holds each timing statistic, by name.
	Timing map[string]Distribution `json:"timing"`
}

// Distribution summarises one timing statistic's samples, in milliseconds.
type Distribution struct {
	// N is how many samples there are.
	N int `json:"n"`
	// P50Ms and P95Ms are the 50th and 95th percentiles, by nearest rank.
	P50Ms int64 `json:"p50_ms"`
	P95Ms int64 `json:"p95_ms"`
}

// The timing statistics.
const (
	// statHandoffLag is how long after its Tenant API call ended each
	// Message's handoff started.
	statHandoffLag = "handoff lag"
	// statAttemptLatency is each Attempt's herald.latency_ms, but for an
	// Endpoint whose answer the harness holds back.
	statAttemptLatency = "Attempt latency"
	// statRetryLateness is herald.lateness_ms of each Attempt but a
	// Delivery's first. A first Attempt's lateness isn't a statistic: when
	// Deliveries are stored on one schedule and Attempts made on another, it
	// is how far apart the two schedules happen to start, nearly the same
	// for every Delivery of a run and different in the next, which no
	// margin narrower than the schedules can compare.
	statRetryLateness = "retry lateness"
)

// heldBack holds the behaviours of an Endpoint that holds back its answer
// to some Attempts, so that an Attempt's latency is the harness's.
var heldBack = []Behaviour{Slow, TimeOut}

// NewStatistics returns the statistics of a run of obs's scenario, from its
// spans.
func NewStatistics(obs Observations, spans []e2e.Span) Statistics {
	c := newSpanCheck(obs, spans)
	st := Statistics{Counts: map[string]int{}}
	samples := map[string][]int64{}
	for _, s := range c.spans {
		outcome := s.Attributes[attrOutcome]
		switch {
		case s.Name == spanTenantAPICall:
			st.Counts[s.Name+" "+s.Attributes[attrRPCMethod]+" "+outcome]++
			continue
		case s.Name == spanHandoffBatch, c.faults[s.Attributes[attrMessageID]] != "":
			continue
		}
		st.Counts[s.Name+" "+outcome]++
		if s.Name != spanAttempt {
			continue
		}
		if ms, err := strconv.ParseInt(s.Attributes[attrLatencyMs], 10, 64); err == nil &&
			!slices.Contains(heldBack, c.behaviours[s.Attributes[attrEndpointID]]) {
			samples[statAttemptLatency] = append(samples[statAttemptLatency], ms)
		}
		if ms, err := strconv.ParseInt(s.Attributes[attrLatenessMs], 10, 64); err == nil && s.Attributes[attrAttempt] != "1" {
			samples[statRetryLateness] = append(samples[statRetryLateness], ms)
		}
	}
	for _, id := range c.messages() {
		if c.faults[id.String()] != "" {
			continue
		}
		handoff, ok := c.handedOff(id.String())
		if !ok {
			continue
		}
		if call, ok := c.root(handoff.TraceID); ok {
			samples[statHandoffLag] = append(samples[statHandoffLag], handoff.Start.Sub(call.End).Milliseconds())
		}
	}
	st.Timing = map[string]Distribution{}
	for name, ms := range samples {
		st.Timing[name] = distribution(ms)
	}
	return st
}

// distribution summarises samples, of which there is at least one.
func distribution(samples []int64) Distribution {
	slices.Sort(samples)
	rank := func(p float64) int64 {
		return samples[int(math.Ceil(p*float64(len(samples))))-1]
	}
	return Distribution{N: len(samples), P50Ms: rank(0.5), P95Ms: rank(0.95)}
}
