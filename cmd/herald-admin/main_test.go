package main

import (
	"bytes"
	"strings"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/ingest/ingesttest"
	"github.com/madskrogh/herald/internal/pgtest"
)

const wantUsage = `usage: herald-admin <command> [arguments]

herald-admin is the operator CLI for Herald. It connects directly to the
database in HERALD_DATABASE_URL.

Commands:
  tenant create --name <name>    Create a Tenant and print its ID
  apikey create --tenant <id>    Create an API key for a Tenant and print it
  help                           Show this help
`

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// TestRun covers the command line without a database: every case fails or
// finishes before connecting.
func TestRun(t *testing.T) {
	t.Parallel()

	dbEnv := map[string]string{"HERALD_DATABASE_URL": "postgres://unused"}
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "no command",
			args:       nil,
			wantCode:   2,
			wantStderr: wantUsage,
		},
		{
			name:       "help",
			args:       []string{"help"},
			wantCode:   0,
			wantStdout: wantUsage,
		},
		{
			name:       "-h",
			args:       []string{"-h"},
			wantCode:   0,
			wantStdout: wantUsage,
		},
		{
			name:       "unknown command",
			args:       []string{"frobnicate"},
			wantCode:   2,
			wantStderr: "herald-admin: unknown command \"frobnicate\"\n\n" + wantUsage,
		},
		{
			name:       "unknown subcommand",
			args:       []string{"tenant", "delete"},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: unknown command \"tenant delete\"\n\n" + wantUsage,
		},
		{
			name:       "tenant create without --name",
			args:       []string{"tenant", "create"},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: tenant create: --name is required\n",
		},
		{
			name:       "tenant create with a blank --name",
			args:       []string{"tenant", "create", "--name", "  "},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: tenant create: --name is required\n",
		},
		{
			name:       "tenant create with an unknown flag",
			args:       []string{"tenant", "create", "--name", "Acme", "--plan", "pro"},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: tenant create: flag provided but not defined: -plan\n",
		},
		{
			name:       "tenant create with extra arguments",
			args:       []string{"tenant", "create", "--name", "Acme", "Corp"},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: tenant create: unexpected argument \"Corp\"\n",
		},
		{
			name:       "apikey create without --tenant",
			args:       []string{"apikey", "create"},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: apikey create: --tenant is required\n",
		},
		{
			name:       "apikey create with a Tenant ID that isn't a UUID",
			args:       []string{"apikey", "create", "--tenant", "acme"},
			env:        dbEnv,
			wantCode:   2,
			wantStderr: "herald-admin: apikey create: --tenant \"acme\" is not a UUID\n",
		},
		{
			name:       "no database URL",
			args:       []string{"tenant", "create", "--name", "Acme"},
			wantCode:   1,
			wantStderr: "herald-admin: HERALD_DATABASE_URL is not set\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := run(t.Context(), tt.args, mapEnv(tt.env), &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, code, tt.wantCode)
			}
			if diff := cmp.Diff(tt.wantStdout, stdout.String()); diff != "" {
				t.Errorf("stdout (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantStderr, stderr.String()); diff != "" {
				t.Errorf("stderr (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCreateTenantAndAPIKey(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	dbURL := ingesttest.NewDatabase(t)
	store, _ := ingesttest.NewStore(t, dbURL)
	env := map[string]string{"HERALD_DATABASE_URL": dbURL}

	printedID := runOK(t, env, "tenant", "create", "--name", "Acme")
	tenantID, err := uuid.Parse(printedID)
	if err != nil {
		t.Fatalf("tenant create printed %q, want a Tenant ID: %v", printedID, err)
	}
	key := runOK(t, env, "apikey", "create", "--tenant", printedID)

	got, err := store.TenantByAPIKey(t.Context(), key)
	if err != nil {
		t.Fatalf("TenantByAPIKey(printed key): %v", err)
	}
	want := ingest.Tenant{ID: tenantID, Name: "Acme"}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(ingest.Tenant{}, "CreatedAt")); diff != "" {
		t.Errorf("Tenant of the printed key (-want +got):\n%s", diff)
	}
}

func TestAPIKeyForUnknownTenant(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := map[string]string{"HERALD_DATABASE_URL": ingesttest.NewDatabase(t)}

	var stdout, stderr bytes.Buffer
	args := []string{"apikey", "create", "--tenant", "0193a1b2-0000-7000-8000-000000000000"}
	code := run(t.Context(), args, mapEnv(env), &stdout, &stderr)

	if code != 1 {
		t.Errorf("run(%q) = %d, want 1", args, code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", &stdout)
	}
	wantStderr := "herald-admin: create api key for tenant 0193a1b2-0000-7000-8000-000000000000: not found\n"
	if diff := cmp.Diff(wantStderr, stderr.String()); diff != "" {
		t.Errorf("stderr (-want +got):\n%s", diff)
	}
}

// runOK runs herald-admin with args, fails the test unless it succeeds, and
// returns its output without the trailing newline.
func runOK(t *testing.T, env map[string]string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), args, mapEnv(env), &stdout, &stderr); code != 0 {
		t.Fatalf("run(%q) = %d, want 0; stderr:\n%s", args, code, &stderr)
	}
	return strings.TrimSuffix(stdout.String(), "\n")
}

// mapEnv returns a getenv function that looks names up in env.
func mapEnv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}
