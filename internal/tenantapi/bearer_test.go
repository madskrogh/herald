package tenantapi

import (
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
)

func FuzzBearerToken(f *testing.F) {
	for _, s := range []string{"Bearer herald_ABC", "bearer x", "Bearer ", "Bearer", "Basic x", "Bearer  x", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs("authorization", value))
		token, ok := bearerToken(ctx)
		if !ok {
			return
		}
		scheme, rest, _ := strings.Cut(value, " ")
		if !strings.EqualFold(scheme, "Bearer") || token == "" || rest != token {
			t.Errorf("bearerToken(%q) = %q, want the non-empty token after \"Bearer \"", value, token)
		}
	})
}
