package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/trace"

	"github.com/madskrogh/herald/internal/telemetry"
)

func TestLogHandlerAddsTheTraceID(t *testing.T) {
	t.Parallel()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36},
		SpanID:     trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7},
		TraceFlags: trace.FlagsSampled,
	})
	tests := []struct {
		name string
		ctx  context.Context
		want map[string]any
	}{
		{
			name: "in a span",
			ctx:  trace.ContextWithSpanContext(t.Context(), sc),
			want: map[string]any{
				"msg":      "attempt",
				"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
				"span_id":  "00f067aa0ba902b7",
				"service":  "herald-dispatcher",
			},
		},
		{
			name: "outside any span",
			ctx:  t.Context(),
			want: map[string]any{"msg": "attempt", "service": "herald-dispatcher"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			json := slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: dropTimeAndLevel})
			logger := slog.New(telemetry.NewLogHandler(json)).With("service", "herald-dispatcher")
			logger.InfoContext(tt.ctx, "attempt")
			if diff := cmp.Diff(tt.want, decodeRecord(t, buf.Bytes())); diff != "" {
				t.Errorf("log record (-want +got):\n%s", diff)
			}
		})
	}
}

func dropTimeAndLevel(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
		return slog.Attr{}
	}
	return a
}

func decodeRecord(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode log record %q: %v", data, err)
	}
	return record
}
