package telemetrytest_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/madskrogh/herald/internal/telemetry/telemetrytest"
)

// TestCheck records spans as a service would, and checks that the
// check finds what breaks the span schema, the type each value is
// emitted as included.
func TestCheck(t *testing.T) {
	t.Parallel()
	const tenantID = "0198a1b2-0000-7000-8000-0000000000aa"
	tests := []struct {
		name string
		// emit emits spans through tracer.
		emit func(tracer trace.Tracer)
		// want is what a problem names, or empty for none.
		want string
	}{
		{
			name: "a Batch that follows the schema",
			emit: func(tracer trace.Tracer) {
				batch(tracer, attribute.Int("herald.batch_size", 2))
			},
		},
		{
			name: "an int emitted as a string, beside another problem",
			emit: func(tracer trace.Tracer) {
				_, s := tracer.Start(context.Background(), "handoff.batch", trace.WithAttributes(
					attribute.String("herald.batch_size", "2"),
					attribute.String("herald.outcome", "handed_off"),
				))
				s.End()
			},
			want: "herald.batch_size",
		},
		{
			name: "an int emitted as a string",
			emit: func(tracer trace.Tracer) {
				batch(tracer, attribute.String("herald.batch_size", "2"))
			},
			want: "herald.batch_size",
		},
		{
			name: "a UUID emitted as an int",
			emit: func(tracer trace.Tracer) {
				_, s := tracer.Start(context.Background(), "tenant_api.call", trace.WithAttributes(
					attribute.String("rpc.method", "/herald.tenant.v1.MessageService/CreateMessage"),
					attribute.Int("herald.tenant_id", 7),
					attribute.String("herald.outcome", "OK"),
					attribute.String("herald.error", ""),
				))
				s.End()
			},
			want: "herald.tenant_id",
		},
		{
			name: "an attribute the span doesn't have",
			emit: func(tracer trace.Tracer) {
				batch(tracer, attribute.Int("herald.batch_size", 2), attribute.String("herald.tenant_id", tenantID))
			},
			want: "herald.tenant_id",
		},
		{
			name: "a parent the span may not have",
			emit: func(tracer trace.Tracer) {
				ctx, call := tracer.Start(context.Background(), "tenant_api.call", trace.WithAttributes(
					attribute.String("rpc.method", "/herald.tenant.v1.MessageService/CreateMessage"),
					attribute.String("herald.tenant_id", tenantID),
					attribute.String("herald.outcome", "OK"),
					attribute.String("herald.error", ""),
				))
				_, b := tracer.Start(ctx, "handoff.batch", trace.WithAttributes(
					attribute.Int("herald.batch_size", 2),
					attribute.String("herald.outcome", "handed_off"),
					attribute.String("herald.error", ""),
				))
				b.End()
				call.End()
			},
			want: `parent "tenant_api.call", want none`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := tracetest.NewSpanRecorder()
			tt.emit(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test"))
			got := telemetrytest.Check(rec.Ended())
			switch {
			case tt.want == "" && len(got) != 0:
				t.Errorf("Check = %v, want none", got)
			case tt.want != "" && !slices.ContainsFunc(got, func(err error) bool { return strings.Contains(err.Error(), tt.want) }):
				t.Errorf("Check = %v, want one naming %s", got, tt.want)
			}
		})
	}
}

// batch emits a handoff.batch span that follows the schema but for its
// Batch size, which is given with any other attributes.
func batch(tracer trace.Tracer, attrs ...attribute.KeyValue) {
	_, s := tracer.Start(context.Background(), "handoff.batch", trace.WithAttributes(
		attribute.String("herald.outcome", "handed_off"),
		attribute.String("herald.error", ""),
	))
	s.SetAttributes(attrs...)
	s.End()
}
