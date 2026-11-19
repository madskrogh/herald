// Package config reads the settings that herald-api and herald-dispatcher
// both read, so that each parses the same way in both services. If they
// parsed HERALD_ALLOW_INSECURE_ENDPOINTS differently, herald-api could accept
// an Endpoint that herald-dispatcher then refuses to deliver to. Each
// service's own settings stay in its main package.
package config

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/telemetry"
)

// Shared is the configuration both services read.
type Shared struct {
	// DatabaseURL is the Postgres connection URL (HERALD_DATABASE_URL).
	DatabaseURL string
	// Keyring holds the keys for signing secrets (HERALD_KEYRING): herald-api
	// encrypts with it and herald-dispatcher decrypts.
	Keyring *keyring.Keyring
	// NATSURL is the NATS server URL (HERALD_NATS_URL).
	NATSURL string
	// AllowInsecureEndpoints relaxes the checks on Endpoints for the local
	// stack and tests (HERALD_ALLOW_INSECURE_ENDPOINTS, default false):
	// herald-api accepts http Endpoint URLs, and herald-dispatcher lets
	// Attempts connect to non-public addresses.
	AllowInsecureEndpoints bool
	// Telemetry says where spans go and what identifies them.
	Telemetry telemetry.Config
}

// Load reads the shared settings through getenv. The database URL, the
// keyring and the NATS URL are required; HERALD_ALLOW_INSECURE_ENDPOINTS and
// the telemetry settings (HERALD_OTLP_URL, HERALD_RUN_ID and
// HERALD_BUILD_VERSION, default "dev") are optional. The first setting
// missing or invalid is reported, checked in this order: the database URL,
// the keyring, the NATS URL, then HERALD_ALLOW_INSECURE_ENDPOINTS.
func Load(getenv func(string) string) (Shared, error) {
	dbURL, err := DatabaseURL(getenv)
	if err != nil {
		return Shared{}, err
	}
	cfg := Shared{DatabaseURL: dbURL}
	keys := getenv("HERALD_KEYRING")
	if keys == "" {
		return Shared{}, errors.New("HERALD_KEYRING is not set")
	}
	kr, err := keyring.Parse(keys)
	if err != nil {
		return Shared{}, fmt.Errorf("HERALD_KEYRING: %w", err)
	}
	cfg.Keyring = kr
	cfg.NATSURL = getenv("HERALD_NATS_URL")
	if cfg.NATSURL == "" {
		return Shared{}, errors.New("HERALD_NATS_URL is not set")
	}
	if s := getenv("HERALD_ALLOW_INSECURE_ENDPOINTS"); s != "" {
		allow, err := strconv.ParseBool(s)
		if err != nil {
			return Shared{}, fmt.Errorf("HERALD_ALLOW_INSECURE_ENDPOINTS: %w", err)
		}
		cfg.AllowInsecureEndpoints = allow
	}
	cfg.Telemetry = telemetry.Config{
		OTLPURL:      getenv("HERALD_OTLP_URL"),
		RunID:        getenv("HERALD_RUN_ID"),
		BuildVersion: getenv("HERALD_BUILD_VERSION"),
	}
	if cfg.Telemetry.BuildVersion == "" {
		cfg.Telemetry.BuildVersion = "dev"
	}
	return cfg, nil
}

// DatabaseURL reads only the database URL through getenv, for commands that
// need nothing else, such as migrate.
func DatabaseURL(getenv func(string) string) (string, error) {
	dbURL := getenv("HERALD_DATABASE_URL")
	if dbURL == "" {
		return "", errors.New("HERALD_DATABASE_URL is not set")
	}
	return dbURL, nil
}
