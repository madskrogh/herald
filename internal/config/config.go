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
	// PprofAddr is the internal address net/http/pprof is served on
	// (HERALD_PPROF_ADDR), separate from the Tenant-facing ones. Empty, the
	// default, serves no pprof.
	PprofAddr string
}

// Load reads the shared settings through getenv. All but
// HERALD_ALLOW_INSECURE_ENDPOINTS and HERALD_PPROF_ADDR are required. The
// first setting missing or invalid is reported, checked in this order: the
// database URL, the keyring, the NATS URL, then
// HERALD_ALLOW_INSECURE_ENDPOINTS.
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
	cfg.PprofAddr = getenv("HERALD_PPROF_ADDR")
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
