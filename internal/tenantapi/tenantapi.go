// Package tenantapi serves the Tenant API (herald.tenant.v1) over gRPC, and
// over REST/JSON through a grpc-gateway that calls the gRPC server.
//
// The gateway reaches the services through a gRPC connection rather than
// calling them in-process, so every REST call passes through the same
// interceptors as a gRPC call: authentication and the mapping of errors to
// status codes exist once, for both protocols.
package tenantapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/textproto"
	"runtime/debug"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/keyring"
)

// NewGRPCServer returns a gRPC server that serves the Tenant API. Signing
// secrets are encrypted with kr, and errors that aren't the caller's are
// logged to logger.
func NewGRPCServer(store *ingest.Store, kr *keyring.Keyring, logger *slog.Logger) *grpc.Server {
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		recoverPanics(logger),
		statusErrors(logger),
		authenticate(store),
	))
	tenantv1.RegisterApplicationServiceServer(srv, &applicationServer{store: store})
	tenantv1.RegisterEndpointServiceServer(srv, &endpointServer{store: store, keyring: kr})
	tenantv1.RegisterMessageServiceServer(srv, &messageServer{store: store})
	return srv
}

// maxRequestBody is the most a REST request body may hold. The gateway reads
// and decodes the whole body before the gRPC server authenticates the call,
// so the limit applies to callers without a valid API key too.
//
// The largest valid body is a CreateMessage request. Its payload travels as a
// JSON string, in which each byte may be escaped as the six bytes \u00XX
// (encoding/json escapes <, > and & this way by default), so the limit is six
// times the largest payload, with 64 KiB for the other fields.
const maxRequestBody = 6*maxPayloadBytes + 64<<10

// NewGateway returns a handler that serves the Tenant API over REST/JSON by
// calling the gRPC server at the other end of conn.
func NewGateway(ctx context.Context, conn grpc.ClientConnInterface) (http.Handler, error) {
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
		// Field names as in the protos (application_id), so that each field
		// has one name in gRPC, REST bodies, query parameters and the docs.
		// Unpopulated fields are written, as by the gateway's default.
		MarshalOptions: protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true},
		// Unlike the default, a field the API doesn't know is an error: a
		// misspelled "filter" would otherwise leave an Endpoint's filter empty,
		// accepting every Event type.
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
	}), runtime.WithErrorHandler(handleError), runtime.WithIncomingHeaderMatcher(matchHeader))
	if err := tenantv1.RegisterApplicationServiceHandlerClient(ctx, mux, tenantv1.NewApplicationServiceClient(conn)); err != nil {
		return nil, fmt.Errorf("register application service: %w", err)
	}
	if err := tenantv1.RegisterEndpointServiceHandlerClient(ctx, mux, tenantv1.NewEndpointServiceClient(conn)); err != nil {
		return nil, fmt.Errorf("register endpoint service: %w", err)
	}
	if err := tenantv1.RegisterMessageServiceHandlerClient(ctx, mux, tenantv1.NewMessageServiceClient(conn)); err != nil {
		return nil, fmt.Errorf("register message service: %w", err)
	}
	return http.MaxBytesHandler(mux, maxRequestBody), nil
}

// matchHeader forwards the Idempotency-Key header as the metadata the gRPC
// server reads it from, and every other header as the gateway does by default.
func matchHeader(key string) (string, bool) {
	if textproto.CanonicalMIMEHeaderKey(key) == "Idempotency-Key" {
		return idempotencyKeyMetadata, true
	}
	return runtime.DefaultHeaderMatcher(key)
}

// handleError writes a gRPC error as a REST response, as the gateway's default
// handler does, except that ResourceExhausted is 413 Content Too Large rather
// than 429 Too Many Requests: the Tenant API returns it only for a payload
// that is too large, which retrying later won't fix.
func handleError(ctx context.Context, mux *runtime.ServeMux, m runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	if status.Code(err) == codes.ResourceExhausted {
		w = statusOverride{ResponseWriter: w, code: http.StatusRequestEntityTooLarge}
	}
	runtime.DefaultHTTPErrorHandler(ctx, mux, m, w, r, err)
}

// statusOverride is a ResponseWriter that writes code as the status, whatever
// status it is asked to write.
type statusOverride struct {
	http.ResponseWriter
	code int
}

func (w statusOverride) WriteHeader(int) {
	w.ResponseWriter.WriteHeader(w.code)
}

// statusErrors is a gRPC interceptor that turns the errors the services
// return into gRPC status errors. Errors that are already status errors are
// the caller's, and pass through. ingest.ErrNotFound becomes NotFound. Any
// other error is Herald's own: it is logged, and the caller sees only
// Internal. Neither passes on the error's message, which is written for
// Herald's logs and may reveal internals.
func statusErrors(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		// The caller cancelled the call or ran out of time: not a failure of
		// Herald's.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		if errors.Is(err, ingest.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "not found")
		}
		logger.ErrorContext(ctx, "internal error", "method", info.FullMethod, "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
}

// recoverPanics is a gRPC interceptor that turns a panic in a call into an
// Internal error, so that one call's panic doesn't end the process and every
// other call with it. grpc-go doesn't recover panics, unlike net/http.
func recoverPanics(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorContext(ctx, "panic", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
