// Package e2e drives a running Herald stack for end-to-end tests: it calls
// the Tenant API as a Tenant does, serves the Endpoints that Herald delivers
// to, and reads where each Delivery ended up.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/ingest"
)

// Config says where the stack is.
type Config struct {
	// APIURL is the Tenant API's REST base URL, such as http://localhost:8080.
	APIURL string
	// DatabaseURL connects to the stack's Postgres. Tenants and API keys are
	// created there, as herald-admin does, and Deliveries are read there
	// until the Delivery API exists (M3).
	DatabaseURL string
	// EndpointAddr is the address the fake Endpoints listen on. It must be
	// reachable from herald-dispatcher's container.
	EndpointAddr string
	// EndpointHost is the host name in the fake Endpoints' URLs: how
	// herald-dispatcher reaches this process.
	EndpointHost string
	// OTLPURL is the Collector's OTLP/HTTP base URL, such as
	// http://localhost:4318.
	OTLPURL string
	// ClickHouseURL is ClickHouse's HTTP interface, with its credentials,
	// such as http://herald:herald@localhost:8123.
	ClickHouseURL string
}

// ConfigFromEnv reads the Config for the compose stack from the environment,
// as `make e2e` sets it. ok is false when HERALD_E2E_API_URL is unset: no
// stack to test.
//
//	HERALD_E2E_API_URL         the Tenant API's REST base URL
//	HERALD_E2E_DATABASE_URL    the stack's Postgres
//	HERALD_E2E_ENDPOINT_IP     the host address the fake Endpoints listen on,
//	                           the one host.docker.internal reaches
//	                           (default 127.0.0.1, as on Docker Desktop)
//	HERALD_E2E_OTLP_URL        the Collector's OTLP/HTTP base URL
//	HERALD_E2E_CLICKHOUSE_URL  ClickHouse's HTTP interface, with credentials
//
// herald-dispatcher reaches the fake Endpoints as host.docker.internal
// (compose.yaml), on a port picked when they start.
func ConfigFromEnv(getenv func(string) string) (cfg Config, ok bool, err error) {
	ip := getenv("HERALD_E2E_ENDPOINT_IP")
	if ip == "" {
		ip = "127.0.0.1"
	}
	cfg = Config{
		APIURL:        getenv("HERALD_E2E_API_URL"),
		DatabaseURL:   getenv("HERALD_E2E_DATABASE_URL"),
		EndpointAddr:  net.JoinHostPort(ip, "0"),
		EndpointHost:  "host.docker.internal",
		OTLPURL:       getenv("HERALD_E2E_OTLP_URL"),
		ClickHouseURL: getenv("HERALD_E2E_CLICKHOUSE_URL"),
	}
	if cfg.APIURL == "" {
		return Config{}, false, nil
	}
	required := []struct{ name, value string }{
		{"HERALD_E2E_DATABASE_URL", cfg.DatabaseURL},
		{"HERALD_E2E_OTLP_URL", cfg.OTLPURL},
		{"HERALD_E2E_CLICKHOUSE_URL", cfg.ClickHouseURL},
	}
	for _, r := range required {
		if r.value == "" {
			return Config{}, false, fmt.Errorf("%s is not set", r.name)
		}
	}
	return cfg, true, nil
}

// Stack is a running Herald stack, with fake Endpoints for it to deliver to.
type Stack struct {
	apiURL        string
	otlpURL       string
	clickHouseURL string
	client        *http.Client
	pool          *pgxpool.Pool
	ingest        *ingest.Store
	deliveries    *delivery.Store
	// Endpoints serves the fake Endpoints that Tenants register.
	Endpoints *endpointtest.Server
}

// Connect connects to the stack that cfg describes and starts the fake
// Endpoints. Close the Stack when done.
func Connect(ctx context.Context, cfg Config) (*Stack, error) {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	endpoints, err := endpointtest.Start(ctx, cfg.EndpointAddr, cfg.EndpointHost)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Stack{
		apiURL:        cfg.APIURL,
		otlpURL:       cfg.OTLPURL,
		clickHouseURL: cfg.ClickHouseURL,
		client:        &http.Client{Timeout: 30 * time.Second},
		pool:          pool,
		ingest:        ingest.NewStore(pool),
		deliveries:    delivery.NewStore(pool),
		Endpoints:     endpoints,
	}, nil
}

// Close stops the fake Endpoints and disconnects from the stack.
func (s *Stack) Close() {
	s.Endpoints.Close()
	s.pool.Close()
}

// NewTenant creates a Tenant with an API key, as herald-admin does.
func (s *Stack) NewTenant(ctx context.Context, name string) (*Tenant, error) {
	tenant, err := s.ingest.CreateTenant(ctx, name)
	if err != nil {
		return nil, err
	}
	key, err := s.ingest.CreateAPIKey(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	return &Tenant{stack: s, apiKey: key}, nil
}

// pollInterval is how often WaitDeliveries reads the Deliveries. The stack
// runs in other processes, so there is nothing to wait on but their state.
const pollInterval = 100 * time.Millisecond

// WaitDeliveries waits until the Message with the given ID has Deliveries
// and none of them is pending, and returns them, ordered by ID.
func (s *Stack) WaitDeliveries(ctx context.Context, messageID string) ([]delivery.Delivery, error) {
	id, err := uuid.Parse(messageID)
	if err != nil {
		return nil, fmt.Errorf("message ID %q: %w", messageID, err)
	}
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		deliveries, err := s.deliveries.MessageDeliveries(ctx, id)
		if err != nil {
			return nil, err
		}
		if len(deliveries) > 0 && !anyPending(deliveries) {
			return deliveries, nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return deliveries, fmt.Errorf("message %s: %d Deliveries, waiting for all to finish: %w", messageID, len(deliveries), context.Cause(ctx))
		}
	}
}

func anyPending(deliveries []delivery.Delivery) bool {
	for _, d := range deliveries {
		if d.State == delivery.StatePending {
			return true
		}
	}
	return false
}

// Tenant calls the Tenant API with one Tenant's API key.
type Tenant struct {
	stack  *Stack
	apiKey string
}

// CreateApplication creates an Application and returns its ID.
func (t *Tenant) CreateApplication(ctx context.Context, name string) (string, error) {
	var resp struct {
		Application struct {
			ID string `json:"id"`
		} `json:"application"`
	}
	err := t.call(ctx, "/v1/applications", map[string]any{"name": name}, nil, &resp)
	return resp.Application.ID, err
}

// CreateEndpoint registers ep under an Application, accepting every Event
// type, and tells ep its signing secret. It returns the Endpoint's ID.
func (t *Tenant) CreateEndpoint(ctx context.Context, applicationID string, ep *endpointtest.Endpoint) (string, error) {
	var resp struct {
		Endpoint struct {
			ID string `json:"id"`
		} `json:"endpoint"`
		SigningSecret string `json:"signing_secret"`
	}
	path := "/v1/applications/" + applicationID + "/endpoints"
	if err := t.call(ctx, path, map[string]any{"url": ep.URL}, nil, &resp); err != nil {
		return "", err
	}
	ep.SetSecret(resp.SigningSecret)
	return resp.Endpoint.ID, nil
}

// SendMessage sends a Message to an Application, with an Idempotency-Key
// unless idempotencyKey is empty, and returns the Message's ID.
func (t *Tenant) SendMessage(ctx context.Context, applicationID, eventType, payload, idempotencyKey string) (string, error) {
	header := http.Header{}
	if idempotencyKey != "" {
		header.Set("Idempotency-Key", idempotencyKey)
	}
	var resp struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	body := map[string]any{"application_id": applicationID, "event_type": eventType, "payload": payload}
	err := t.call(ctx, "/v1/messages", body, header, &resp)
	return resp.Message.ID, err
}

// call POSTs body as JSON to path and decodes the response into resp.
func (t *Tenant) call(ctx context.Context, path string, body any, header http.Header, resp any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("POST %s: encode request: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.stack.apiURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := t.stack.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer res.Body.Close()
	data, err = io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("POST %s: read response: %w", path, err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s: %s: %s", path, res.Status, data)
	}
	if err := json.Unmarshal(data, resp); err != nil {
		return fmt.Errorf("POST %s: decode response: %w", path, err)
	}
	return nil
}
