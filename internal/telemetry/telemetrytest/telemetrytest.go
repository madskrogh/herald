// Package telemetrytest records the spans that the code under test emits,
// and checks each against the span schema (internal/telemetry).
package telemetrytest

import (
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/madskrogh/herald/internal/telemetry"
)

// Recorder records the spans ended through its Provider.
type Recorder struct {
	// Provider is the TracerProvider to give the code under test.
	Provider *sdktrace.TracerProvider
	rec      *tracetest.SpanRecorder
}

// NewRecorder returns a Recorder that records every span. When t finishes,
// each span it recorded must match the schema, or t fails: every test that
// records spans checks the schema too.
func NewRecorder(t *testing.T) *Recorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	r := &Recorder{
		Provider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)),
		rec:      rec,
	}
	t.Cleanup(func() {
		for _, s := range r.rec.Ended() {
			if err := telemetry.CheckSpan(s.Name(), attributeKeys(s)); err != nil {
				t.Errorf("span schema: %v", err)
			}
		}
	})
	return r
}

// Spans returns the spans ended so far, in the order they ended.
func (r *Recorder) Spans() []Span {
	var spans []Span
	for _, s := range r.rec.Ended() {
		span := Span{
			Name:       s.Name(),
			Context:    s.SpanContext(),
			Parent:     s.Parent(),
			Attributes: map[string]string{},
		}
		for _, kv := range s.Attributes() {
			span.Attributes[string(kv.Key)] = kv.Value.Emit()
		}
		for _, l := range s.Links() {
			span.Links = append(span.Links, l.SpanContext)
		}
		spans = append(spans, span)
	}
	return spans
}

// Named returns the spans ended so far with the given name.
func (r *Recorder) Named(name string) []Span {
	var spans []Span
	for _, s := range r.Spans() {
		if s.Name == name {
			spans = append(spans, s)
		}
	}
	return spans
}

// Span is a recorded span, as much of it as tests compare.
type Span struct {
	Name    string
	Context trace.SpanContext
	// Parent is the parent span's context, invalid for a root span.
	Parent trace.SpanContext
	Links  []trace.SpanContext
	// Attributes holds each attribute's value as text.
	Attributes map[string]string
}

func attributeKeys(s sdktrace.ReadOnlySpan) []string {
	var keys []string
	for _, kv := range s.Attributes() {
		keys = append(keys, string(kv.Key))
	}
	return keys
}
