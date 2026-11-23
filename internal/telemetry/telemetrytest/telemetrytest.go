// Package telemetrytest records the spans that the code under test emits,
// and checks each against the span schema in the spec (spec/span-schema.md).
package telemetrytest

import (
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/madskrogh/herald/internal/spanschema"
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
		for _, err := range Check(r.rec.Ended()) {
			t.Errorf("span schema: %v", err)
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
			span.Attributes[string(kv.Key)] = kv.Value.String()
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

// Check returns every way spans break the span schema, the type each value
// was emitted as included: an int attribute must be emitted as an integer,
// and any other as a string. A span's parent and links are checked by name
// when they are among spans; its service isn't checked.
func Check(spans []sdktrace.ReadOnlySpan) []error {
	schema, err := spanschema.Load()
	if err != nil {
		return []error{err}
	}
	names := map[trace.SpanID]string{}
	for _, s := range spans {
		names[s.SpanContext().SpanID()] = s.Name()
	}
	var problems []error
	for _, s := range spans {
		span := spanschema.Span{
			Name:       s.Name(),
			Attributes: map[string]string{},
			Parent:     names[s.Parent().SpanID()],
			Root:       !s.Parent().IsValid(),
		}
		for _, kv := range s.Attributes() {
			span.Attributes[string(kv.Key)] = kv.Value.String()
		}
		for _, l := range s.Links() {
			if name, ok := names[l.SpanContext.SpanID()]; ok {
				span.Links = append(span.Links, name)
			}
		}
		got := schema.Check(span)
		if len(got) == 0 {
			got = emittedTypes(schema, s)
		}
		problems = append(problems, got...)
	}
	return problems
}

// emittedTypes returns the attributes of s, which follows the schema, that
// were emitted as another type than the schema gives.
func emittedTypes(schema spanschema.Schema, s sdktrace.ReadOnlySpan) []error {
	var problems []error
	for _, kv := range s.Attributes() {
		want := attribute.STRING
		if schema.Attributes[string(kv.Key)].Type == spanschema.Int {
			want = attribute.INT64
		}
		if got := kv.Value.Type(); got != want {
			problems = append(problems, fmt.Errorf("span %q: attribute %q emitted as %s, want %s", s.Name(), kv.Key, got, want))
		}
	}
	return problems
}
