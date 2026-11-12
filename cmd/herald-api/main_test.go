package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest/ingesttest"
)

// logRecord holds the fields of a JSON log line that the tests check.
type logRecord struct {
	Level    string `json:"level"`
	Msg      string `json:"msg"`
	Service  string `json:"service"`
	Version  int64  `json:"version"`
	GRPCAddr string `json:"grpc_addr"`
	HTTPAddr string `json:"http_addr"`
}

// testKeyring is a keyring with one key of 32 zero bytes, under the ID "test".
var testKeyring = "test:" + base64.StdEncoding.EncodeToString(make([]byte, 32))

func TestRunServesTheTenantAPIUntilCancelled(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	dbURL := ingesttest.NewDatabase(t)
	store, _ := ingesttest.NewStore(t, dbURL)
	tenant, err := store.CreateTenant(t.Context(), "Acme")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	key, err := store.CreateAPIKey(t.Context(), tenant.ID)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	env := map[string]string{
		"HERALD_DATABASE_URL": dbURL,
		"HERALD_KEYRING":      testKeyring,
		// Port 0 picks a free port; the "started" log line says which.
		"HERALD_GRPC_ADDR": "127.0.0.1:0",
		"HERALD_HTTP_ADDR": "127.0.0.1:0",
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	logr, logw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := run(ctx, nil, mapEnv(env), logw)
		logw.Close()
		errc <- err
	}()
	logs := make(chan logRecord)
	go func() {
		defer close(logs)
		scanner := bufio.NewScanner(logr)
		for scanner.Scan() {
			var rec logRecord
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				rec = logRecord{Msg: "not JSON: " + scanner.Text()}
			}
			logs <- rec
		}
	}()

	started, ok := <-logs
	if !ok {
		t.Fatalf("run exited before it started: %v", <-errc)
	}
	if started.Msg != "started" {
		t.Fatalf("first log line is %+v, want started", started)
	}

	t.Run("REST", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+started.HTTPAddr+"/v1/applications", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /v1/applications: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET /v1/applications: status %d, want 200", res.StatusCode)
		}
	})

	t.Run("gRPC", func(t *testing.T) {
		conn, err := grpc.NewClient(started.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+key)
		if _, err := tenantv1.NewApplicationServiceClient(conn).ListApplications(ctx, &tenantv1.ListApplicationsRequest{}); err != nil {
			t.Errorf("ListApplications: %v", err)
		}
	})

	cancel()
	var rest []logRecord
	for rec := range logs {
		rest = append(rest, rec)
	}
	if err := <-errc; err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []logRecord{{Level: "INFO", Msg: "stopped", Service: "herald-api"}}
	if diff := cmp.Diff(want, rest); diff != "" {
		t.Errorf("log after started (-want +got):\n%s", diff)
	}
}

// decodeLog parses r as a stream of JSON log lines and fails the test if any
// line isn't JSON.
func decodeLog(t *testing.T, r io.Reader) []logRecord {
	t.Helper()
	var records []logRecord
	dec := json.NewDecoder(r)
	for {
		var rec logRecord
		err := dec.Decode(&rec)
		if errors.Is(err, io.EOF) {
			return records
		}
		if err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		records = append(records, rec)
	}
}
