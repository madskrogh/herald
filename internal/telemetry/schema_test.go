package telemetry_test

import (
	"testing"

	"github.com/madskrogh/herald/internal/telemetry"
)

func TestCheckSpan(t *testing.T) {
	t.Parallel()
	attempt := []string{
		"herald.tenant_id", "herald.application_id", "herald.endpoint_id",
		"herald.message_id", "herald.delivery_id", "herald.attempt",
		"herald.outcome", "herald.status_code", "herald.latency_ms", "herald.error",
	}
	tests := []struct {
		name    string
		span    string
		keys    []string
		wantErr bool
	}{
		{name: "every key the schema lists", span: "attempt", keys: attempt},
		{name: "keys in another order", span: "attempt", keys: reversed(attempt)},
		{name: "a key missing", span: "attempt", keys: attempt[1:], wantErr: true},
		{name: "a key not in the schema", span: "attempt", keys: append(attempt[:len(attempt):len(attempt)], "herald.endpoint_url"), wantErr: true},
		{name: "a key twice", span: "attempt", keys: append(attempt[:len(attempt):len(attempt)], "herald.error"), wantErr: true},
		{name: "a span name not in the schema", span: "send attempt", keys: attempt, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := telemetry.CheckSpan(tt.span, tt.keys)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckSpan(%q, %q) = %v, want error: %t", tt.span, tt.keys, err, tt.wantErr)
			}
		})
	}
}

func reversed(s []string) []string {
	r := make([]string, len(s))
	for i, v := range s {
		r[len(s)-1-i] = v
	}
	return r
}
