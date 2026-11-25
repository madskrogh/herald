package scenario

import (
	"slices"
	"testing"

	"github.com/madskrogh/herald/internal/spanschema"
)

// TestStatisticsReadOnlyWhatTheSchemaDeclares checks that every span name,
// attribute key and outcome NewStatistics reads is one the span schema in
// the spec declares for that span, so the statistics can't read what a
// build isn't bound to emit.
func TestStatisticsReadOnlyWhatTheSchemaDeclares(t *testing.T) {
	t.Parallel()
	schema, err := spanschema.Load()
	if err != nil {
		t.Fatal(err)
	}
	reads := []struct {
		span       string
		attributes []string
		outcomes   []string
	}{
		{span: spanTenantAPICall, attributes: []string{attrRPCMethod, attrOutcome}},
		{span: spanHandoffBatch},
		{span: spanHandoff, attributes: []string{attrMessageID, attrOutcome}, outcomes: []string{outcomeHandedOff}},
		{span: spanStoreDeliveries, attributes: []string{attrMessageID, attrOutcome}},
		{span: spanAttempt, attributes: []string{
			attrMessageID, attrEndpointID, attrAttempt, attrOutcome, attrLatencyMs, attrLatenessMs,
		}},
	}
	for _, r := range reads {
		s, ok := schema.Spans[r.span]
		if !ok {
			t.Errorf("the span schema has no span %q", r.span)
			continue
		}
		for _, a := range r.attributes {
			if !slices.Contains(s.Attributes, a) {
				t.Errorf("the span schema gives span %q no attribute %q", r.span, a)
			}
		}
		for _, o := range r.outcomes {
			if !slices.Contains(s.Outcomes, o) {
				t.Errorf("the span schema gives span %q no outcome %q", r.span, o)
			}
		}
	}
}
