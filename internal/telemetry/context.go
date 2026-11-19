package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// traceContext reads and writes trace context as W3C traceparent.
var traceContext propagation.TraceContext

// TraceParent returns the context of the span in ctx as a W3C traceparent, to
// store with work that continues the trace later, or empty if ctx has no
// span.
//
// Messages and Deliveries wait in Postgres, where a request's context is
// lost, so each keeps the traceparent it was created under.
func TraceParent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	traceContext.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// Start starts a span named name as a child of the span whose context
// traceparent holds. If traceparent is empty or invalid, as for work stored
// before trace context was, the span starts a trace of its own, whatever
// span ctx holds.
func Start(ctx context.Context, tracer trace.Tracer, traceparent, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	sc := traceContext.Extract(context.Background(), propagation.MapCarrier{"traceparent": traceparent})
	if parent := trace.SpanContextFromContext(sc); parent.IsValid() {
		ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
	} else {
		opts = append(opts, trace.WithNewRoot())
	}
	return tracer.Start(ctx, name, opts...)
}

// End records how the span's work ended, as its outcome and error, and ends
// it. err is nil for work that succeeded.
func End(span trace.Span, outcome string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
		span.SetStatus(codes.Error, msg)
	}
	span.SetAttributes(Outcome.String(outcome), Error.String(msg))
	span.End()
}
