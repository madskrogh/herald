package tenantapi

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/madskrogh/herald/internal/telemetry"
)

// tracerName names the tracer of the Tenant API's spans.
const tracerName = "github.com/madskrogh/herald/internal/tenantapi"

// traceCalls is a gRPC interceptor that makes a span of each call, the root
// of a trace: a Message created in the call keeps its context, and the rest
// of the Message's journey joins that trace. REST calls reach the gRPC server
// through the gateway, so they get one span each too.
//
// The span is always a new root. Trace context a Tenant sends is ignored, so
// a Tenant can't choose Herald's trace IDs or turn its sampling off.
func traceCalls(tp trace.TracerProvider) grpc.UnaryServerInterceptor {
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	tracer := tp.Tracer(tracerName)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, span := tracer.Start(ctx, telemetry.SpanTenantAPICall,
			trace.WithNewRoot(),
			trace.WithSpanKind(trace.SpanKindServer),
			// Until authenticate finds the Tenant.
			trace.WithAttributes(telemetry.RPCMethod.String(info.FullMethod), telemetry.TenantID.String("")),
		)
		defer span.End()
		resp, err := handler(ctx, req)
		// The interceptors after this one have made err a status error.
		st := status.Convert(err)
		span.SetAttributes(
			telemetry.Outcome.String(st.Code().String()),
			telemetry.Error.String(st.Message()),
		)
		if err != nil {
			span.SetStatus(codes.Error, st.Message())
		}
		return resp, err
	}
}
