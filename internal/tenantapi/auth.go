package tenantapi

import (
	"context"
	"errors"
	"strings"
	"uuid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/madskrogh/herald/internal/ingest"
)

// tenantIDKey is the context key for the ID of the Tenant making a call.
type tenantIDKey struct{}

// authenticate is a gRPC interceptor that resolves the API key in a call's
// "authorization: Bearer <key>" metadata to its Tenant, whose ID the services
// read with tenantID. The REST gateway forwards the HTTP Authorization header
// as that metadata.
func authenticate(store *ingest.Store) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		key, ok := bearerToken(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, `want an API key as "authorization: Bearer <key>"`)
		}
		tenant, err := store.TenantByAPIKey(ctx, key)
		if errors.Is(err, ingest.ErrNotFound) {
			return nil, status.Error(codes.Unauthenticated, "invalid API key")
		}
		if err != nil {
			return nil, err
		}
		return handler(context.WithValue(ctx, tenantIDKey{}, tenant.ID), req)
	}
}

// bearerToken returns the token in the call's one "authorization" metadata
// value, which must use the Bearer scheme.
func bearerToken(ctx context.Context) (string, bool) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	// Authentication schemes are case-insensitive (RFC 9110).
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// tenantID returns the ID of the Tenant that authenticate found for the call.
func tenantID(ctx context.Context) uuid.UUID {
	id, ok := ctx.Value(tenantIDKey{}).(uuid.UUID)
	if !ok {
		// Every service method runs behind authenticate.
		panic("tenantapi: call has no authenticated Tenant")
	}
	return id
}
