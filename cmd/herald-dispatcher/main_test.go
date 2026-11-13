package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/madskrogh/herald/internal/delivery"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/natstest"
	"github.com/madskrogh/herald/internal/pgtest"
)

// logRecord holds the fields of a JSON log line that the tests check.
type logRecord struct {
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Service string `json:"service"`
	Version int64  `json:"version"`
}

// TestRunStoresHandedOffBatchesUntilCancelled sends a Batch on the subject
// herald-api's relay uses. It is the only test in this package on that
// subject, so it needs no subject of its own.
func TestRunStoresHandedOffBatchesUntilCancelled(t *testing.T) {
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
		"HERALD_NATS_URL":     natstest.URL(t),
	}

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

	started, ok := <-logs
	if !ok {
		t.Fatalf("run exited before it started: %v", stop())
	}
	if started.Msg != "started" {
		t.Fatalf("first log line is %+v, want started", started)
	}

	const deliveryID = "0190a5e4-0000-7000-8000-0000000000d1"
	data, err := proto.Marshal(&handoffv1.Batch{Messages: []*handoffv1.Message{{
		Id:        "0190a5e4-0000-7000-8000-0000000000a1",
		TenantId:  "0190a5e4-0000-7000-8000-000000000001",
		EventType: "invoice.paid",
		Payload:   []byte(`{"invoice":42}`),
		Deliveries: []*handoffv1.Delivery{{
			Id: deliveryID,
			Endpoint: &handoffv1.Endpoint{
				Id:             "0190a5e4-0000-7000-8000-0000000000e1",
				Url:            "https://example.com/webhooks",
				SigningSecrets: []*handoffv1.SigningSecret{{Ciphertext: []byte("secret")}},
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
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	got, err := delivery.NewStore(pool).Delivery(t.Context(), uuid.MustParse(deliveryID))
	if err != nil {
		t.Fatalf("Delivery: %v", err)
	}
	if got.State != delivery.StatePending {
		t.Errorf("Delivery state is %q, want %q", got.State, delivery.StatePending)
	}

	if err := stop(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []logRecord{{Level: "INFO", Msg: "stopped", Service: "herald-dispatcher"}}
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
