// Package e2e drives a running Herald stack for end-to-end tests: it calls
// the Tenant API as a Tenant does, serves the Endpoints that Herald delivers
// to, and reads where each Delivery ended up.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/e2e/e2edb"
	"github.com/madskrogh/herald/internal/endpointtest"
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
	// APIPprofURL and DispatcherPprofURL are where herald-api and
	// herald-dispatcher serve pprof, such as http://localhost:6060. The
	// scenario reads their goroutine profiles; the end-to-end tests don't.
	APIPprofURL, DispatcherPprofURL string
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
//	HERALD_E2E_API_PPROF_URL   herald-api's pprof, for the scenario
//	HERALD_E2E_DISPATCHER_PPROF_URL
//	                           herald-dispatcher's pprof, for the scenario
//
// herald-dispatcher reaches the fake Endpoints as host.docker.internal
// (compose.yaml), on a port picked when they start.
func ConfigFromEnv(getenv func(string) string) (cfg Config, ok bool, err error) {
	ip := getenv("HERALD_E2E_ENDPOINT_IP")
	if ip == "" {
		ip = "127.0.0.1"
	}
	cfg = Config{
		APIURL:             getenv("HERALD_E2E_API_URL"),
		DatabaseURL:        getenv("HERALD_E2E_DATABASE_URL"),
		EndpointAddr:       net.JoinHostPort(ip, "0"),
		EndpointHost:       "host.docker.internal",
		OTLPURL:            getenv("HERALD_E2E_OTLP_URL"),
		ClickHouseURL:      getenv("HERALD_E2E_CLICKHOUSE_URL"),
		APIPprofURL:        getenv("HERALD_E2E_API_PPROF_URL"),
		DispatcherPprofURL: getenv("HERALD_E2E_DISPATCHER_PPROF_URL"),
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
	// pprofURLs holds where each service serves pprof, by service name.
	pprofURLs map[string]string
	pool      *pgxpool.Pool
	postgres  *e2edb.Queries
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
		pprofURLs: map[string]string{
			"herald-api":        cfg.APIPprofURL,
			"herald-dispatcher": cfg.DispatcherPprofURL,
		},
		pool:      pool,
		postgres:  e2edb.New(pool),
		Endpoints: endpoints,
	}, nil
}

// Close stops the fake Endpoints and disconnects from the stack.
func (s *Stack) Close() {
	s.Endpoints.Close()
	s.pool.Close()
}

// NewTenant creates a Tenant with an API key, writing only the columns the
// column list says the scenario sets, as herald-admin does (R-85).
func (s *Stack) NewTenant(ctx context.Context, name string) (*Tenant, error) {
	id, err := s.postgres.CreateTenant(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("create tenant %q: %w", name, err)
	}
	// Any key works: the Tenant API accepts a key whose SHA-256 is stored
	// for a Tenant (the column list's ingest.api_keys.key_hash).
	key := rand.Text()
	hash := sha256.Sum256([]byte(key))
	err = s.postgres.CreateAPIKey(ctx, e2edb.CreateAPIKeyParams{TenantID: id, KeyHash: hash[:]})
	if err != nil {
		return nil, fmt.Errorf("create api key for tenant %s: %w", id, err)
	}
	return &Tenant{ID: id, stack: s, apiKey: key}, nil
}

// pollInterval is how often poll checks the stack's state. The stack runs in
// other processes, so there is nothing to wait on but their state.
const pollInterval = 100 * time.Millisecond

// poll calls check every pollInterval until it reports done or fails, or
// ctx ends.
func poll(ctx context.Context, check func() (done bool, err error)) error {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// WaitDeliveries waits until the Message with the given ID has Deliveries
// and none of them is pending, and returns them, ordered by ID.
func (s *Stack) WaitDeliveries(ctx context.Context, messageID string) ([]Delivery, error) {
	id, err := uuid.Parse(messageID)
	if err != nil {
		return nil, fmt.Errorf("message ID %q: %w", messageID, err)
	}
	var deliveries []Delivery
	err = poll(ctx, func() (bool, error) {
		var err error
		deliveries, err = s.MessageDeliveries(ctx, id)
		return len(deliveries) > 0 && !anyPending(deliveries), err
	})
	switch {
	case err != nil && ctx.Err() == nil:
		return nil, err
	case err != nil:
		return deliveries, fmt.Errorf("message %s: %d Deliveries, waiting for all to finish: %w", messageID, len(deliveries), err)
	}
	return deliveries, nil
}

// GoroutineProfile returns the service's goroutine profile, in pprof's
// debug=1 text. Each request has a connection of its own, closed after it,
// so the profiles a caller compares don't count a connection kept from the
// one before.
func (s *Stack) GoroutineProfile(ctx context.Context, service string) ([]byte, error) {
	base := s.pprofURLs[service]
	if base == "" {
		return nil, fmt.Errorf("goroutine profile of %s: no pprof URL for it", service)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/debug/pprof/goroutine?debug=1", nil)
	if err != nil {
		return nil, fmt.Errorf("goroutine profile of %s: %w", service, err)
	}
	req.Close = true
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("goroutine profile of %s: %w", service, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("goroutine profile of %s: read: %w", service, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("goroutine profile of %s: %w", service, &StatusError{Code: res.StatusCode, Body: data})
	}
	return data, nil
}

// CloseIdleConnections closes the connections to the Tenant API that the
// Stack keeps open between calls, so that herald-api stops serving them.
func (s *Stack) CloseIdleConnections() {
	s.client.CloseIdleConnections()
}

// Tenant calls the Tenant API with one Tenant's API key.
type Tenant struct {
	ID     uuid.UUID
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
	return t.CreateFilteredEndpoint(ctx, applicationID, ep, nil)
}

// CreateFilteredEndpoint registers ep under an Application, accepting the
// Event types filter matches, and tells ep its signing secret. It returns
// the Endpoint's ID.
func (t *Tenant) CreateFilteredEndpoint(ctx context.Context, applicationID string, ep *endpointtest.Endpoint, filter []string) (string, error) {
	var resp struct {
		Endpoint struct {
			ID string `json:"id"`
		} `json:"endpoint"`
		SigningSecret string `json:"signing_secret"`
	}
	path := "/v1/applications/" + applicationID + "/endpoints"
	if err := t.call(ctx, path, map[string]any{"url": ep.URL, "filter": filter}, nil, &resp); err != nil {
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
	header = header.Clone()
	if header == nil {
		header = http.Header{}
	}
	header.Set("Authorization", "Bearer "+t.apiKey)
	header.Set("Content-Type", "application/json")
	data, err = t.stack.post(ctx, t.stack.apiURL+path, header, data)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	if err := json.Unmarshal(data, resp); err != nil {
		return fmt.Errorf("POST %s: decode response: %w", path, err)
	}
	return nil
}

// StatusError is the error for a response with any status but 200 OK.
type StatusError struct {
	Code int
	Body []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Code, http.StatusText(e.Code), e.Body)
}

// post POSTs body to target with the given header, and returns the response
// body, or a *StatusError for any status but 200 OK.
func (s *Stack) post(ctx context.Context, target string, header http.Header, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = header
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: res.StatusCode, Body: data}
	}
	return data, nil
}
