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

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/keyring"
)

// NewGRPCServer returns a gRPC server that serves the Tenant API. Signing
// secrets are encrypted with kr, and errors that aren't the caller's are
// logged to logger.
func NewGRPCServer(store *ingest.Store, kr *keyring.Keyring, logger *slog.Logger) *grpc.Server {
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		statusErrors(logger),
		authenticate(store),
	))
	tenantv1.RegisterApplicationServiceServer(srv, &applicationServer{store: store})
	tenantv1.RegisterEndpointServiceServer(srv, &endpointServer{store: store, keyring: kr})
	return srv
}

// NewGateway returns a handler that serves the Tenant API over REST/JSON by
// calling the gRPC server at the other end of conn.
func NewGateway(ctx context.Context, conn grpc.ClientConnInterface) (http.Handler, error) {
	mux := runtime.NewServeMux()
	if err := tenantv1.RegisterApplicationServiceHandlerClient(ctx, mux, tenantv1.NewApplicationServiceClient(conn)); err != nil {
		return nil, fmt.Errorf("register application service: %w", err)
	}
	if err := tenantv1.RegisterEndpointServiceHandlerClient(ctx, mux, tenantv1.NewEndpointServiceClient(conn)); err != nil {
		return nil, fmt.Errorf("register endpoint service: %w", err)
	}
	return mux, nil
}

// statusErrors is a gRPC interceptor that turns the errors the services
// return into gRPC status errors. Errors that are already status errors are
// the caller's, and pass through. ingest.ErrNotFound becomes NotFound. Any
// other error is Herald's own: it is logged, and the caller sees only
// Internal, since its message may reveal internals.
func statusErrors(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if errors.Is(err, ingest.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		logger.ErrorContext(ctx, "internal error", "method", info.FullMethod, "error", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
}
