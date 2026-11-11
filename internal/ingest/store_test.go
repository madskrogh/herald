package ingest_test

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"

	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/ingest/ingesttest"
	"github.com/madskrogh/herald/internal/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

func TestAPIKeyResolvesToItsTenant(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := t.Context()
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))

	tenant, err := store.CreateTenant(ctx, "Acme")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	key, err := store.CreateAPIKey(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	got, err := store.TenantByAPIKey(ctx, key)
	if err != nil {
		t.Fatalf("TenantByAPIKey: %v", err)
	}
	if diff := cmp.Diff(tenant, got); diff != "" {
		t.Errorf("TenantByAPIKey (-want +got):\n%s", diff)
	}
}

func TestTenantNameMustNotBeBlank(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))

	for _, name := range []string{"", " \t "} {
		if tenant, err := store.CreateTenant(t.Context(), name); err == nil {
			t.Errorf("CreateTenant(%q) created Tenant %s, want an error", name, tenant.ID)
		}
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))

	tests := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "API key that was never created",
			call: func(ctx context.Context) error {
				_, err := store.TenantByAPIKey(ctx, "herald_AAAAAAAAAAAAAAAAAAAAAAAAAA")
				return err
			},
		},
		{
			name: "API key for a Tenant that doesn't exist",
			call: func(ctx context.Context) error {
				_, err := store.CreateAPIKey(ctx, uuid.MustParse("0193a1b2-0000-7000-8000-000000000000"))
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.call(t.Context()); !errors.Is(err, ingest.ErrNotFound) {
				t.Errorf("error = %v, want ingest.ErrNotFound", err)
			}
		})
	}
}

func TestAPIKeys(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := t.Context()
	store, pool := ingesttest.NewStore(t, ingesttest.NewDatabase(t))

	tenant, err := store.CreateTenant(ctx, "Acme")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	first, err := store.CreateAPIKey(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	second, err := store.CreateAPIKey(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	t.Run("have a recognizable prefix and 128 random bits", func(t *testing.T) {
		t.Parallel()
		format := regexp.MustCompile(`^herald_[A-Z2-7]{26}$`)
		for _, key := range []string{first, second} {
			if !format.MatchString(key) {
				t.Errorf("key %q doesn't match %s", key, format)
			}
		}
	})

	t.Run("are unique", func(t *testing.T) {
		t.Parallel()
		if first == second {
			t.Errorf("two keys are both %q", first)
		}
	})

	// The one test that reads a table directly: what is stored is the
	// property under test, and the Store has no way to show it.
	t.Run("are not stored in plain", func(t *testing.T) {
		t.Parallel()
		// A row's text form shows bytea as hex, so the hash column is also
		// checked as raw bytes.
		type apiKeyRow struct {
			Text    string
			KeyHash []byte
		}
		rows, err := pool.Query(t.Context(), "SELECT k::text, k.key_hash FROM ingest.api_keys k")
		if err != nil {
			t.Fatalf("query api_keys: %v", err)
		}
		stored, err := pgx.CollectRows(rows, pgx.RowToStructByPos[apiKeyRow])
		if err != nil {
			t.Fatalf("read api_keys: %v", err)
		}
		if len(stored) != 2 {
			t.Fatalf("got %d api_keys rows, want 2", len(stored))
		}
		for _, row := range stored {
			for _, key := range []string{first, second} {
				// The random part alone, in case the prefix is stripped.
				secret := strings.TrimPrefix(key, "herald_")
				if strings.Contains(row.Text, secret) || bytes.Contains(row.KeyHash, []byte(secret)) {
					t.Errorf("api_keys row %s contains the key %q", row.Text, key)
				}
			}
		}
	})
}
