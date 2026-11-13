package delivery_test

import (
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/madskrogh/herald/internal/delivery"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/natstest"
	"github.com/madskrogh/herald/internal/pgtest"
	"github.com/madskrogh/herald/internal/testmain"
)

func TestMain(m *testing.M) {
	testmain.Run(m, pgtest.Start, natstest.Start)
}

// now is the intake's clock in these tests. Postgres keeps microseconds, so
// it has none smaller.
var now = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

// replyTimeout is how long a test waits for the intake's reply.
const replyTimeout = 10 * time.Second

// IDs in the test Batch.
const (
	tenantID    = "0190a5e4-0000-7000-8000-000000000001"
	messageID1  = "0190a5e4-0000-7000-8000-0000000000a1"
	messageID2  = "0190a5e4-0000-7000-8000-0000000000a2"
	deliveryID1 = "0190a5e4-0000-7000-8000-0000000000d1"
	deliveryID2 = "0190a5e4-0000-7000-8000-0000000000d2"
	deliveryID3 = "0190a5e4-0000-7000-8000-0000000000d3"
	endpointID1 = "0190a5e4-0000-7000-8000-0000000000e1"
	endpointID2 = "0190a5e4-0000-7000-8000-0000000000e2"
)

// testBatch returns a Batch of two Messages: the first to two Endpoints, the
// second to one of them.
func testBatch() *handoffv1.Batch {
	endpoint1 := &handoffv1.Endpoint{
		Id:             endpointID1,
		Url:            "https://example.com/webhooks",
		SigningSecrets: []*handoffv1.SigningSecret{{Ciphertext: []byte("secret-1a")}, {Ciphertext: []byte("secret-1b")}},
	}
	endpoint2 := &handoffv1.Endpoint{
		Id:             endpointID2,
		Url:            "https://example.org/hooks?app=7",
		SigningSecrets: []*handoffv1.SigningSecret{{Ciphertext: []byte("secret-2")}},
	}
	return &handoffv1.Batch{Messages: []*handoffv1.Message{
		{
			Id:        messageID1,
			TenantId:  tenantID,
			EventType: "invoice.paid",
			Payload:   []byte(`{"invoice": 42}`),
			Deliveries: []*handoffv1.Delivery{
				{Id: deliveryID1, Endpoint: endpoint1},
				{Id: deliveryID2, Endpoint: endpoint2},
			},
		},
		{
			Id:         messageID2,
			TenantId:   tenantID,
			EventType:  "invoice.voided",
			Payload:    []byte(`{"invoice":43}`),
			Deliveries: []*handoffv1.Delivery{{Id: deliveryID3, Endpoint: endpoint1}},
		},
	}}
}

// The Deliveries in testBatch, as stored.
var testDeliveries = []delivery.Delivery{
	{
		ID:        uuid.MustParse(deliveryID1),
		MessageID: uuid.MustParse(messageID1),
		TenantID:  uuid.MustParse(tenantID),
		EventType: "invoice.paid",
		Payload:   []byte(`{"invoice": 42}`),
		Endpoint: delivery.Endpoint{
			ID:             uuid.MustParse(endpointID1),
			URL:            "https://example.com/webhooks",
			SigningSecrets: [][]byte{[]byte("secret-1a"), []byte("secret-1b")},
		},
		State:         delivery.StatePending,
		NextAttemptAt: now,
	},
	{
		ID:        uuid.MustParse(deliveryID2),
		MessageID: uuid.MustParse(messageID1),
		TenantID:  uuid.MustParse(tenantID),
		EventType: "invoice.paid",
		Payload:   []byte(`{"invoice": 42}`),
		Endpoint: delivery.Endpoint{
			ID:             uuid.MustParse(endpointID2),
			URL:            "https://example.org/hooks?app=7",
			SigningSecrets: [][]byte{[]byte("secret-2")},
		},
		State:         delivery.StatePending,
		NextAttemptAt: now,
	},
	{
		ID:        uuid.MustParse(deliveryID3),
		MessageID: uuid.MustParse(messageID2),
		TenantID:  uuid.MustParse(tenantID),
		EventType: "invoice.voided",
		Payload:   []byte(`{"invoice":43}`),
		Endpoint: delivery.Endpoint{
			ID:             uuid.MustParse(endpointID1),
			URL:            "https://example.com/webhooks",
			SigningSecrets: [][]byte{[]byte("secret-1a"), []byte("secret-1b")},
		},
		State:         delivery.StatePending,
		NextAttemptAt: now,
	},
}

func TestIntakeStoresABatch(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t)

	got := in.send(t, testBatch())

	if diff := cmp.Diff(&handoffv1.BatchReply{Committed: true}, got, cmpProto); diff != "" {
		t.Errorf("reply (-want +got):\n%s", diff)
	}
	// The relay marks the Messages handed off on this reply, so the Batch
	// must be committed by now: these reads are on another connection than
	// the intake's.
	for _, want := range testDeliveries {
		got, err := in.store.Delivery(t.Context(), want.ID)
		if err != nil {
			t.Errorf("Delivery(%s): %v", want.ID, err)
			continue
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Delivery(%s) (-want +got):\n%s", want.ID, diff)
		}
	}
	// Each payload is stored once, on its Message, not on every Delivery.
	if diff := cmp.Diff(rowCounts{Messages: 2, Deliveries: 3}, in.countRows(t)); diff != "" {
		t.Errorf("rows (-want +got):\n%s", diff)
	}
}

// TestIntakeStoresAResentBatchOnce sends a Batch twice, as a relay does when
// a reply is lost.
func TestIntakeStoresAResentBatchOnce(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t)

	for i := range 2 {
		got := in.send(t, testBatch())
		if diff := cmp.Diff(&handoffv1.BatchReply{Committed: true}, got, cmpProto); diff != "" {
			t.Errorf("send %d: reply (-want +got):\n%s", i+1, diff)
		}
	}

	if diff := cmp.Diff(rowCounts{Messages: 2, Deliveries: 3}, in.countRows(t)); diff != "" {
		t.Errorf("rows (-want +got):\n%s", diff)
	}
}

func TestIntakeRejectsABatchItCannotStore(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}

	// Each case breaks testBatch in its second Message, so the first is
	// valid: none of the Batch may be stored.
	encode := func(t *testing.T, change func(b *handoffv1.Batch)) []byte {
		t.Helper()
		batch := testBatch()
		change(batch)
		data, err := proto.Marshal(batch)
		if err != nil {
			t.Fatalf("encode batch: %v", err)
		}
		return data
	}
	tests := []struct {
		name string
		data func(t *testing.T) []byte
	}{
		{
			name: "not a Batch",
			data: func(*testing.T) []byte { return []byte("not protobuf") },
		},
		{
			name: "Message ID that isn't a UUID",
			data: func(t *testing.T) []byte {
				return encode(t, func(b *handoffv1.Batch) { b.Messages[1].Id = "message-2" })
			},
		},
		{
			name: "Delivery ID that isn't a UUID",
			data: func(t *testing.T) []byte {
				return encode(t, func(b *handoffv1.Batch) { b.Messages[1].Deliveries[0].Id = "" })
			},
		},
		{
			// Postgres rejects this one, after the first Message is inserted.
			name: "Endpoint without signing secrets",
			data: func(t *testing.T) []byte {
				return encode(t, func(b *handoffv1.Batch) {
					b.Messages[1].Deliveries[0].Endpoint = &handoffv1.Endpoint{
						Id:             endpointID1,
						Url:            "https://example.com/webhooks",
						SigningSecrets: []*handoffv1.SigningSecret{},
					}
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := newTestIntake(t)

			got := in.sendBytes(t, tt.data(t))

			if got.GetCommitted() || got.GetError() == "" {
				t.Errorf("reply is %v, want committed = false with an error", got)
			}
			if diff := cmp.Diff(rowCounts{}, in.countRows(t)); diff != "" {
				t.Errorf("rows (-want +got):\n%s", diff)
			}
		})
	}
}

// testIntake is an Intake subscribed to a subject of its own, on a database
// of its own.
type testIntake struct {
	nc      *nats.Conn
	subject string
	pool    *pgxpool.Pool
	store   *delivery.Store
}

func newTestIntake(t *testing.T) *testIntake {
	t.Helper()
	dbURL := pgtest.NewDatabase(t)
	if err := delivery.Migrate(t.Context(), dbURL, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// The intake and the test have pools of their own, so the test reads
	// what the intake stored on another connection, as a worker would.
	intakePool, pool := newPool(t, dbURL), newPool(t, dbURL)

	nc := natstest.Connect(t)
	subject := natstest.Subject(t)
	intake := delivery.NewIntake(delivery.NewStore(intakePool), func() time.Time { return now }, slog.New(slog.DiscardHandler))
	sub, err := intake.Subscribe(t.Context(), nc, subject, "herald-dispatcher")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return &testIntake{nc: nc, subject: subject, pool: pool, store: delivery.NewStore(pool)}
}

// newPool returns a pool on the database at dbURL, closed when t finishes.
func newPool(t *testing.T, dbURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// send sends batch to the intake, as the relay does, and returns the reply.
func (in *testIntake) send(t *testing.T, batch *handoffv1.Batch) *handoffv1.BatchReply {
	t.Helper()
	data, err := proto.Marshal(batch)
	if err != nil {
		t.Fatalf("encode batch: %v", err)
	}
	return in.sendBytes(t, data)
}

// sendBytes sends data to the intake as a Batch and returns the reply.
func (in *testIntake) sendBytes(t *testing.T, data []byte) *handoffv1.BatchReply {
	t.Helper()
	msg, err := in.nc.Request(in.subject, data, replyTimeout)
	if err != nil {
		t.Fatalf("send batch: %v", err)
	}
	reply := &handoffv1.BatchReply{}
	if err := proto.Unmarshal(msg.Data, reply); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	return reply
}

// rowCounts is how many rows the delivery schema's tables hold.
type rowCounts struct {
	Messages, Deliveries int
}

// countRows counts the rows in the delivery schema. How often a payload is
// stored, and whether a resend or a failed Batch adds rows, are properties of
// storage that the Store deliberately doesn't show, so these are fixed,
// read-only SELECTs rather than sqlc queries.
func (in *testIntake) countRows(t *testing.T) rowCounts {
	t.Helper()
	var c rowCounts
	if err := in.pool.QueryRow(t.Context(), "SELECT count(*) FROM delivery.messages").Scan(&c.Messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := in.pool.QueryRow(t.Context(), "SELECT count(*) FROM delivery.deliveries").Scan(&c.Deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return c
}

// cmpProto compares protobuf messages by content.
var cmpProto = cmp.Comparer(proto.Equal)
