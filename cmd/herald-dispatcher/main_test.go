package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/madskrogh/herald/internal/delivery"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/natstest"
	"github.com/madskrogh/herald/internal/pgtest"
	"github.com/madskrogh/herald/internal/signing"
)

// logRecord holds the fields of a JSON log line that the tests check.
type logRecord struct {
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Service string `json:"service"`
	Version int64  `json:"version"`
}

// testKeyring is the dispatcher's keyring in these tests.
const testKeyring = "test:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// TestLoadSettingsRetryScale checks that the retry schedule runs at full length
// unless configured to shrink, by the delay before a Delivery's second
// Attempt: 5 seconds, scaled, give or take 10% jitter.
func TestLoadSettingsRetryScale(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		value    string // unset if empty
		min, max time.Duration
		wantErr  bool
	}{
		{name: "unset", min: 4500 * time.Millisecond, max: 5500 * time.Millisecond},
		{name: "1", value: "1", min: 4500 * time.Millisecond, max: 5500 * time.Millisecond},
		{name: "0.001", value: "0.001", min: 4500 * time.Microsecond, max: 5500 * time.Microsecond},
		{name: "zero", value: "0", wantErr: true},
		{name: "above 1", value: "2", wantErr: true},
		{name: "not a number", value: "fast", wantErr: true},
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
				env["HERALD_RETRY_SCALE"] = tt.value
			}
			cfg, err := loadSettings(mapEnv(env))
			if tt.wantErr {
				if err == nil {
					t.Errorf("loadSettings accepted %q, want an error", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadSettings: %v", err)
			}
			got, ok := cfg.schedule.Next(1)
			if !ok || got < tt.min || got > tt.max {
				t.Errorf("schedule.Next(1) = %v, %t; want between %v and %v, true", got, ok, tt.min, tt.max)
			}
		})
	}
}

// TestRunDeliversHandedOffBatchesUntilCancelled sends a Batch on the subject
// herald-api's relay uses, with one Delivery to a local Endpoint, and checks
// that it arrives signed. It is the only test in this package on that
// subject, so it needs no subject of its own.
func TestRunDeliversHandedOffBatchesUntilCancelled(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	dbURL := pgtest.NewDatabase(t)
	if err := delivery.Migrate(t.Context(), dbURL, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	env := map[string]string{
		"HERALD_DATABASE_URL": dbURL,
		"HERALD_KEYRING":      testKeyring,
		"HERALD_NATS_URL":     natstest.URL(t),
		// The Endpoint is on a loopback address.
		"HERALD_ALLOW_INSECURE_ENDPOINTS": "true",
		"HERALD_RETRY_SCALE":              "0.5",
	}
	type received struct {
		header http.Header
		body   []byte
	}
	requests := make(chan received, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- received{header: r.Header.Clone(), body: body}
	}))
	defer endpoint.Close()

	ctx, cancel := context.WithCancel(t.Context())
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
	// stop cancels run, reads the log lines left, and waits for run to
	// return. It runs on every exit, so a test that fails early doesn't leave
	// run blocked writing to the log, holding the database open.
	var rest []logRecord
	stop := sync.OnceValue(func() error {
		cancel()
		for rec := range logs {
			rest = append(rest, rec)
		}
		return <-errc
	})
	defer stop() //nolint:errcheck // Only for an early return; checked below.

	// HERALD_ALLOW_INSECURE_ENDPOINTS and HERALD_RETRY_SCALE are set, so a
	// production deployment that sets either by mistake says so as it starts.
	for _, want := range []logRecord{
		{Level: "WARN", Msg: "insecure Endpoints allowed: for local development only", Service: "herald-dispatcher"},
		{Level: "WARN", Msg: "retry schedule shrunk: for local development only", Service: "herald-dispatcher"},
	} {
		warning, ok := <-logs
		if !ok {
			t.Fatalf("run exited before it started: %v", stop())
		}
		if warning != want {
			t.Fatalf("log line is %+v, want %+v", warning, want)
		}
	}
	started, ok := <-logs
	if !ok {
		t.Fatalf("run exited before it started: %v", stop())
	}
	if started.Msg != "started" {
		t.Fatalf("first log line is %+v, want started", started)
	}

	const (
		deliveryID    = "0190a5e4-0000-7000-8000-0000000000d1"
		endpointID    = "0190a5e4-0000-7000-8000-0000000000e1"
		signingSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
		payload       = `{"invoice":42}`
	)
	keys, err := keyring.Parse(testKeyring)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	ciphertext, err := keys.Encrypt([]byte(signingSecret), []byte(endpointID))
	if err != nil {
		t.Fatalf("encrypt signing secret: %v", err)
	}
	data, err := proto.Marshal(&handoffv1.Batch{Messages: []*handoffv1.Message{{
		Id:        "0190a5e4-0000-7000-8000-0000000000a1",
		TenantId:  "0190a5e4-0000-7000-8000-000000000001",
		EventType: "invoice.paid",
		Payload:   []byte(payload),
		Deliveries: []*handoffv1.Delivery{{
			Id: deliveryID,
			Endpoint: &handoffv1.Endpoint{
				Id:             endpointID,
				Url:            endpoint.URL + "/webhooks",
				SigningSecrets: []*handoffv1.SigningSecret{{Ciphertext: ciphertext}},
			},
		}},
	}}})
	if err != nil {
		t.Fatalf("encode batch: %v", err)
	}
	msg, err := natstest.Connect(t).Request("herald.handoff.v1", data, 10*time.Second)
	if err != nil {
		t.Fatalf("send batch: %v", err)
	}
	reply := &handoffv1.BatchReply{}
	if err := proto.Unmarshal(msg.Data, reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if !reply.GetCommitted() {
		t.Errorf("reply is %v, want committed", reply)
	}
	var req received
	select {
	case req = <-requests:
	case <-time.After(10 * time.Second):
		t.Fatal("no Attempt reached the Endpoint within 10s")
	}
	if err := signing.Verify(signingSecret, req.header, req.body, time.Now()); err != nil {
		t.Errorf("verify webhook: %v", err)
	}
	if got := string(req.body); got != payload {
		t.Errorf("body = %q, want %q", got, payload)
	}

	// Shutdown lets the Attempt be recorded before run returns.
	if err := stop(); err != nil {
		t.Fatalf("run: %v", err)
	}
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	got, err := delivery.NewStore(pool).Delivery(t.Context(), uuid.MustParse(deliveryID))
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StateSucceeded {
		t.Errorf("Delivery state is %q, want %q", got.State, delivery.StateSucceeded)
	}
	want := []logRecord{
		{Level: "INFO", Msg: "attempt", Service: "herald-dispatcher"},
		{Level: "INFO", Msg: "stopped", Service: "herald-dispatcher"},
	}
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
