package config_test

import (
	"encoding/base64"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/madskrogh/herald/internal/config"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/telemetry"
)

// testKeyring is a keyring with one key of 32 zero bytes, under the ID "test".
var testKeyring = "test:" + base64.StdEncoding.EncodeToString(make([]byte, 32))

func TestLoad(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"HERALD_DATABASE_URL": "postgres://localhost/herald",
		"HERALD_KEYRING":      testKeyring,
		"HERALD_NATS_URL":     "nats://localhost:4222",
	}
	got, err := config.Load(mapEnv(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Shared{
		DatabaseURL: "postgres://localhost/herald",
		NATSURL:     "nats://localhost:4222",
		Telemetry:   telemetry.Config{BuildVersion: "dev"},
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(config.Shared{}, "Keyring")); diff != "" {
		t.Errorf("Load (-want +got):\n%s", diff)
	}
	checkKeyring(t, got.Keyring)
}

// TestDatabaseURL checks that the database URL is read alone, without the
// settings only serving the service needs.
func TestDatabaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "set",
			env:  map[string]string{"HERALD_DATABASE_URL": "postgres://localhost/herald"},
			want: "postgres://localhost/herald",
		},
		{
			name:    "unset",
			env:     map[string]string{"HERALD_KEYRING": testKeyring, "HERALD_NATS_URL": "nats://unused"},
			wantErr: "HERALD_DATABASE_URL is not set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.DatabaseURL(mapEnv(tt.env))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("DatabaseURL = %q, want error %q", got, tt.wantErr)
				}
				if diff := cmp.Diff(tt.wantErr, err.Error()); diff != "" {
					t.Errorf("DatabaseURL error (-want +got):\n%s", diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("DatabaseURL: %v", err)
			}
			if got != tt.want {
				t.Errorf("DatabaseURL = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLoadAllowInsecureEndpoints checks that insecure Endpoints are allowed
// only when configured.
func TestLoadAllowInsecureEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   string // unset if empty
		want    bool
		wantErr string
	}{
		{name: "unset"},
		{name: "true", value: "true", want: true},
		{name: "1", value: "1", want: true},
		{name: "false", value: "false"},
		{name: "invalid", value: "yes please", wantErr: `HERALD_ALLOW_INSECURE_ENDPOINTS: strconv.ParseBool: parsing "yes please": invalid syntax`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{
				"HERALD_DATABASE_URL": "postgres://localhost/herald",
				"HERALD_KEYRING":      testKeyring,
				"HERALD_NATS_URL":     "nats://localhost:4222",
			}
			if tt.value != "" {
				env["HERALD_ALLOW_INSECURE_ENDPOINTS"] = tt.value
			}
			cfg, err := config.Load(mapEnv(env))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load accepted %q, want error %q", tt.value, tt.wantErr)
				}
				if diff := cmp.Diff(tt.wantErr, err.Error()); diff != "" {
					t.Errorf("Load error (-want +got):\n%s", diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.AllowInsecureEndpoints != tt.want {
				t.Errorf("AllowInsecureEndpoints = %t, want %t", cfg.AllowInsecureEndpoints, tt.want)
			}
		})
	}
}

// TestLoadTelemetry checks the settings for exporting spans, all of them
// optional.
func TestLoadTelemetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want telemetry.Config
	}{
		{
			name: "unset",
			want: telemetry.Config{BuildVersion: "dev"},
		},
		{
			name: "set",
			env: map[string]string{
				"HERALD_OTLP_URL":      "http://otelcol:4318",
				"HERALD_RUN_ID":        "run-42",
				"HERALD_BUILD_VERSION": "86b342f",
			},
			want: telemetry.Config{OTLPURL: "http://otelcol:4318", RunID: "run-42", BuildVersion: "86b342f"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{
				"HERALD_DATABASE_URL": "postgres://localhost/herald",
				"HERALD_KEYRING":      testKeyring,
				"HERALD_NATS_URL":     "nats://localhost:4222",
			}
			for k, v := range tt.env {
				env[k] = v
			}
			cfg, err := config.Load(mapEnv(env))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if diff := cmp.Diff(tt.want, cfg.Telemetry); diff != "" {
				t.Errorf("Telemetry (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLoadErrors checks that each required setting is reported by name, in a
// fixed order: the database URL, the keyring, then the NATS URL.
func TestLoadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "nothing set",
			wantErr: "HERALD_DATABASE_URL is not set",
		},
		{
			name:    "without a database URL",
			env:     map[string]string{"HERALD_KEYRING": testKeyring, "HERALD_NATS_URL": "nats://unused"},
			wantErr: "HERALD_DATABASE_URL is not set",
		},
		{
			name:    "without a keyring",
			env:     map[string]string{"HERALD_DATABASE_URL": "postgres://unused", "HERALD_NATS_URL": "nats://unused"},
			wantErr: "HERALD_KEYRING is not set",
		},
		{
			// The error names the key but must not show it.
			name:    "with an invalid keyring",
			env:     map[string]string{"HERALD_DATABASE_URL": "postgres://unused", "HERALD_NATS_URL": "nats://unused", "HERALD_KEYRING": "test:c2VjcmV0"},
			wantErr: `HERALD_KEYRING: keyring entry 0: key "test" has 6 bytes, want 32`,
		},
		{
			name:    "without a NATS URL",
			env:     map[string]string{"HERALD_DATABASE_URL": "postgres://unused", "HERALD_KEYRING": testKeyring},
			wantErr: "HERALD_NATS_URL is not set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load(mapEnv(tt.env))
			if err == nil {
				t.Fatalf("Load succeeded, want error %q", tt.wantErr)
			}
			if diff := cmp.Diff(tt.wantErr, err.Error()); diff != "" {
				t.Errorf("Load error (-want +got):\n%s", diff)
			}
		})
	}
}

// checkKeyring checks that kr holds the key in testKeyring: it decrypts
// "secret", encrypted once with that key.
func checkKeyring(t *testing.T, kr *keyring.Keyring) {
	t.Helper()
	if kr == nil {
		t.Fatal("Keyring is nil")
	}
	ciphertext, err := base64.StdEncoding.DecodeString("BHRlc3ReP+eJ05oidNKAnGAHlW8/rfnopZxbqkHogHgrE0PUvTOB")
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	got, err := kr.Decrypt(ciphertext, nil)
	if err != nil {
		t.Fatalf("Keyring doesn't decrypt with the configured key: %v", err)
	}
	if string(got) != "secret" {
		t.Errorf("Decrypt = %q, want %q", got, "secret")
	}
}

// mapEnv returns a getenv function that looks names up in env.
func mapEnv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}
