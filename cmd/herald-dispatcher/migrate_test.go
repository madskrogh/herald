package main

import (
	"bytes"
	"errors"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/natstest"
	"github.com/madskrogh/herald/internal/pgtest"
	"github.com/madskrogh/herald/internal/testmain"
)

func TestMain(m *testing.M) {
	testmain.Run(m, pgtest.Start, natstest.Start)
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	dbURL := pgtest.NewDatabase(t)
	env := map[string]string{"HERALD_DATABASE_URL": dbURL}

	// The second run finds nothing to apply, as when the migrate step runs
	// before every start of the stack.
	wantLogs := [][]logRecord{
		{
			{Level: "INFO", Msg: "applied migration", Service: "herald-dispatcher", Version: 1},
			{Level: "INFO", Msg: "schema up to date", Service: "herald-dispatcher", Version: 1},
		},
		{
			{Level: "INFO", Msg: "schema up to date", Service: "herald-dispatcher", Version: 1},
		},
	}
	for i, want := range wantLogs {
		var stdout bytes.Buffer
		if err := run(t.Context(), []string{"migrate"}, mapEnv(env), &stdout); err != nil {
			t.Fatalf("run migrate: %v\nlog:\n%s", err, &stdout)
		}
		if diff := cmp.Diff(want, decodeLog(t, &stdout)); diff != "" {
			t.Errorf("run %d: log (-want +got):\n%s", i+1, diff)
		}
	}

	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	id := uuid.New()
	if _, err := delivery.NewStore(pool).Delivery(t.Context(), id); !errors.Is(err, delivery.ErrNotFound) {
		t.Errorf("Delivery(%s) after migrate: error %v, want %v", id, err, delivery.ErrNotFound)
	}
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "migrate without a database URL",
			args:    []string{"migrate"},
			wantErr: "HERALD_DATABASE_URL is not set",
		},
		{
			name:    "migrate with extra arguments",
			args:    []string{"migrate", "down"},
			env:     map[string]string{"HERALD_DATABASE_URL": "postgres://unused"},
			wantErr: `migrate: unexpected argument "down"`,
		},
		{
			name:    "serve without a database URL",
			env:     map[string]string{"HERALD_NATS_URL": "nats://unused"},
			wantErr: "HERALD_DATABASE_URL is not set",
		},
		{
			name:    "serve without a NATS URL",
			env:     map[string]string{"HERALD_DATABASE_URL": "postgres://unused"},
			wantErr: "HERALD_NATS_URL is not set",
		},
		{
			name:    "unknown command",
			args:    []string{"frobnicate"},
			wantErr: `unknown command "frobnicate"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			err := run(t.Context(), tt.args, mapEnv(tt.env), &stdout)
			if err == nil {
				t.Fatalf("run(%q) succeeded, want error %q", tt.args, tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantErr, err.Error()); diff != "" {
				t.Errorf("run(%q) error (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// mapEnv returns a getenv function that looks names up in env.
func mapEnv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}
