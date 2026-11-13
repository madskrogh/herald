package outbox_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/eventtype"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/natstest"
	"github.com/madskrogh/herald/internal/outbox"
	"github.com/madskrogh/herald/internal/pgtest"
	"github.com/madskrogh/herald/internal/testmain"
)

func TestMain(m *testing.M) {
	// The leak check runs once the containers are gone, so their clients'
	// goroutines have stopped.
	goleak.VerifyTestMain(testRun(func() int {
		return testmain.Tests(m, pgtest.Start, natstest.Start)
	}))
}

// testRun is a function that runs the tests and returns the exit code, as
// goleak.VerifyTestMain takes them.
type testRun func() int

func (f testRun) Run() int { return f() }

// accepted is when the tests' Messages are accepted, and handedOff when the
// relay hands them off. Postgres keeps microseconds, so they have none
// smaller.
var (
	accepted  = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	handedOff = time.Date(2026, 11, 13, 9, 0, 5, 0, time.UTC)
)

// stored is when the dispatcher's intake stores the Deliveries, at which they
// are due.
var stored = time.Date(2026, 11, 13, 9, 0, 6, 0, time.UTC)

func TestRelayHandsOffAMessageWithItsDeliveries(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	env.dispatcher(t, env.subject)
	app := env.application(t)
	endpoint1 := app.endpoint(t, "https://example.com/webhooks", nil)
	endpoint2 := app.endpoint(t, "https://example.org/hooks?app=7", []string{"invoice.*"})
	app.endpoint(t, "https://example.net/customers", []string{"customer.>"})
	msg := app.message(t, "invoice.paid", []byte(`{"invoice": 42}`))
	relay := env.relay(t, outbox.Config{})

	n, err := relay.RunOnce(t.Context(), handedOff)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("RunOnce handed off %d Messages, want 1", n)
	}

	want := []delivery.Delivery{
		{
			MessageID: msg.ID,
			TenantID:  app.tenantID,
			EventType: "invoice.paid",
			Payload:   []byte(`{"invoice": 42}`),
			Endpoint: delivery.Endpoint{
				ID:             endpoint1,
				URL:            "https://example.com/webhooks",
				SigningSecrets: [][]byte{secretFor(endpoint1)},
			},
			State:         delivery.StatePending,
			NextAttemptAt: stored,
		},
		{
			MessageID: msg.ID,
			TenantID:  app.tenantID,
			EventType: "invoice.paid",
			Payload:   []byte(`{"invoice": 42}`),
			Endpoint: delivery.Endpoint{
				ID:             endpoint2,
				URL:            "https://example.org/hooks?app=7",
				SigningSecrets: [][]byte{secretFor(endpoint2)},
			},
			State:         delivery.StatePending,
			NextAttemptAt: stored,
		},
	}
	if diff := cmp.Diff(want, env.storedDeliveries(t, msg), byEndpointURL); diff != "" {
		t.Errorf("stored Deliveries (-want +got):\n%s", diff)
	}

	// Handed off, the Message has left the outbox.
	n, err = relay.RunOnce(t.Context(), handedOff)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("second RunOnce handed off %d Messages, want 0", n)
	}
}

func TestRelayKeepsMessagesWhenTheBatchIsNotCommitted(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	app := env.application(t)
	app.endpoint(t, "https://example.com/webhooks", nil)
	msg := app.message(t, "invoice.paid", []byte(`{"invoice": 42}`))
	relay := env.relay(t, outbox.Config{})
	refuse := env.respond(t, env.subject, func(*handoffv1.Batch) *handoffv1.BatchReply {
		return &handoffv1.BatchReply{Committed: false, Error: "database unavailable"}
	})

	n, err := relay.RunOnce(t.Context(), handedOff)
	if err == nil {
		t.Error("RunOnce succeeded on a Batch that was not committed, want an error")
	}
	if n != 0 {
		t.Errorf("RunOnce handed off %d Messages, want 0", n)
	}

	// The Message waits in the outbox, and is sent again once a dispatcher
	// commits it.
	if err := refuse.Unsubscribe(); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	env.dispatcher(t, env.subject)
	n, err = relay.RunOnce(t.Context(), handedOff)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("second RunOnce handed off %d Messages, want 1", n)
	}
	if got := len(env.storedDeliveries(t, msg)); got != 1 {
		t.Errorf("stored %d Deliveries, want 1", got)
	}
}

// TestRelayResendsABatchWhoseReplyIsLost loses the reply to a Batch the
// dispatcher committed. The relay sends it again, and the dispatcher keeps
// one copy of each Delivery.
func TestRelayResendsABatchWhoseReplyIsLost(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	app := env.application(t)
	app.endpoint(t, "https://example.com/webhooks", nil)
	app.endpoint(t, "https://example.org/hooks", nil)
	msg := app.message(t, "invoice.paid", []byte(`{"invoice": 42}`))
	relay := env.relay(t, outbox.Config{RequestTimeout: time.Second})
	// The dispatcher listens on a subject of its own, and the relay's subject
	// forwards to it, losing the first reply.
	dispatcherSubject := natstest.Subject(t)
	env.dispatcher(t, dispatcherSubject)
	var batches atomic.Int32
	env.respond(t, env.subject, func(batch *handoffv1.Batch) *handoffv1.BatchReply {
		reply := env.forward(t, dispatcherSubject, batch)
		if batches.Add(1) == 1 {
			return nil
		}
		return reply
	})

	n, err := relay.RunOnce(t.Context(), handedOff)
	if err == nil {
		t.Error("RunOnce succeeded without a reply, want an error")
	}
	if n != 0 {
		t.Errorf("RunOnce handed off %d Messages, want 0", n)
	}
	n, err = relay.RunOnce(t.Context(), handedOff)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("second RunOnce handed off %d Messages, want 1", n)
	}

	if got := batches.Load(); got != 2 {
		t.Errorf("the relay sent %d Batches, want 2", got)
	}
	if got := len(env.storedDeliveries(t, msg)); got != 2 {
		t.Errorf("stored %d Deliveries, want 2", got)
	}
	if got, want := env.storedDeliveryCount(t), 2; got != want {
		t.Errorf("the delivery schema holds %d Deliveries, want %d", got, want)
	}
}

func TestRelayDrainsTheBacklogOnceADispatcherStarts(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	app := env.application(t)
	app.endpoint(t, "https://example.com/webhooks", nil)
	var backlog []ingest.Message
	for i := range 3 {
		backlog = append(backlog, app.message(t, "invoice.paid", fmt.Appendf(nil, `{"invoice": %d}`, i)))
	}
	relay := env.relay(t, outbox.Config{})

	// No dispatcher is running.
	n, err := relay.RunOnce(t.Context(), handedOff)
	if err == nil {
		t.Error("RunOnce succeeded with no dispatcher running, want an error")
	}
	if n != 0 {
		t.Errorf("RunOnce handed off %d Messages, want 0", n)
	}

	env.dispatcher(t, env.subject)
	n, err = relay.RunOnce(t.Context(), handedOff)
	if err != nil {
		t.Fatalf("RunOnce with a dispatcher: %v", err)
	}
	if n != 3 {
		t.Errorf("RunOnce with a dispatcher handed off %d Messages, want 3", n)
	}
	for _, msg := range backlog {
		if got := len(env.storedDeliveries(t, msg)); got != 1 {
			t.Errorf("Message %s: stored %d Deliveries, want 1", msg.ID, got)
		}
	}
}

// TestConcurrentRelaysSendDifferentMessages holds one relay's Batch in flight
// while a second relay runs a round. The second relay must send another
// Message, not the one in flight.
func TestConcurrentRelaysSendDifferentMessages(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	app := env.application(t)
	app.endpoint(t, "https://example.com/webhooks", nil)
	msg1 := app.message(t, "invoice.paid", []byte(`{"invoice": 1}`))
	msg2 := app.message(t, "invoice.paid", []byte(`{"invoice": 2}`))
	cfg := outbox.Config{MaxMessages: 1}
	relay1, relay2 := env.relay(t, cfg), env.relay(t, cfg)

	// In front of the dispatcher, a responder records which Messages are in
	// flight, and holds the first Batch until it is released.
	dispatcherSubject := natstest.Subject(t)
	env.dispatcher(t, dispatcherSubject)
	var (
		mu       sync.Mutex
		inFlight = map[string]bool{}
		sent     [][]string // the Message IDs of each Batch
		overlaps []string   // Message IDs sent while already in flight
	)
	held, release := make(chan struct{}), make(chan struct{})
	nc := natstest.Connect(t)
	sub, err := nc.Subscribe(env.subject, func(m *nats.Msg) {
		// Each Batch is answered on a goroutine of its own, so holding one
		// doesn't hold up the next.
		go func() {
			var batch handoffv1.Batch
			if err := proto.Unmarshal(m.Data, &batch); err != nil {
				t.Errorf("decode batch: %v", err)
				return
			}
			var ids []string
			for _, msg := range batch.GetMessages() {
				ids = append(ids, msg.GetId())
			}
			mu.Lock()
			for _, id := range ids {
				if inFlight[id] {
					overlaps = append(overlaps, id)
				}
				inFlight[id] = true
			}
			sent = append(sent, ids)
			first := len(sent) == 1
			mu.Unlock()
			if first {
				close(held)
				<-release
			}
			reply := env.forward(t, dispatcherSubject, &batch)
			mu.Lock()
			for _, id := range ids {
				delete(inFlight, id)
			}
			mu.Unlock()
			data, err := proto.Marshal(reply)
			if err != nil {
				t.Errorf("encode reply: %v", err)
				return
			}
			_ = m.Respond(data)
		}()
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	type result struct {
		n   int
		err error
	}
	first := make(chan result, 1)
	go func() {
		n, err := relay1.RunOnce(t.Context(), handedOff)
		first <- result{n, err}
	}()
	<-held
	n, err := relay2.RunOnce(t.Context(), handedOff)
	close(release)
	if err != nil {
		t.Errorf("second relay's RunOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("second relay handed off %d Messages, want 1", n)
	}
	r := <-first
	if r.err != nil {
		t.Errorf("first relay's RunOnce: %v", r.err)
	}
	if r.n != 1 {
		t.Errorf("first relay handed off %d Messages, want 1", r.n)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(overlaps) > 0 {
		t.Errorf("Messages sent while already in flight: %v", overlaps)
	}
	// The oldest Message goes first.
	want := [][]string{{msg1.ID.String()}, {msg2.ID.String()}}
	if diff := cmp.Diff(want, sent); diff != "" {
		t.Errorf("Batches sent (-want +got):\n%s", diff)
	}
}

// TestRelayKeepsBatchesUnderTheMaximumMessageSize hands off the largest
// Messages Herald accepts: a 256 KiB payload to 100 Endpoints, each with a
// 2048-byte URL. One takes about 480 KB in a Batch, so three don't fit under
// NATS's 1 MB maximum, and they go in more than one round.
func TestRelayKeepsBatchesUnderTheMaximumMessageSize(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	env := newEnv(t)
	env.dispatcher(t, env.subject)
	app := env.application(t)
	for i := range ingest.MaxEndpoints {
		prefix := fmt.Sprintf("https://example.com/%03d/", i)
		app.endpoint(t, prefix+strings.Repeat("a", 2048-len(prefix)), nil)
	}
	payload := []byte(`"` + strings.Repeat("x", 256<<10-2) + `"`)
	var messages []ingest.Message
	for range 3 {
		messages = append(messages, app.message(t, "invoice.paid", payload))
	}
	relay := env.relay(t, outbox.Config{})

	var rounds []int
	for total := 0; total < len(messages); {
		n, err := relay.RunOnce(t.Context(), handedOff)
		if err != nil {
			t.Fatalf("round %d: RunOnce: %v", len(rounds)+1, err)
		}
		if n == 0 {
			t.Fatalf("round %d handed off nothing, with %d Messages left", len(rounds)+1, len(messages)-total)
		}
		rounds = append(rounds, n)
		total += n
	}

	// Two fit in a Batch, and the third waits for the next round.
	if diff := cmp.Diff([]int{2, 1}, rounds); diff != "" {
		t.Errorf("Messages handed off per round (-want +got):\n%s", diff)
	}
	for _, msg := range messages {
		if got := len(env.storedDeliveries(t, msg)); got != ingest.MaxEndpoints {
			t.Errorf("Message %s: stored %d Deliveries, want %d", msg.ID, got, ingest.MaxEndpoints)
		}
	}
}

// byEndpointURL compares Deliveries in any order: a Message's Deliveries are
// in Endpoint ID order, and the tests' Endpoint IDs are random.
var byEndpointURL = cmpopts.SortSlices(func(a, b delivery.Delivery) bool {
	return a.Endpoint.URL < b.Endpoint.URL
})

// env is one test's ingest and delivery schemas, in a database of its own,
// and a NATS subject of its own.
type env struct {
	ingest   *ingest.Store
	delivery *delivery.Store
	pool     *pgxpool.Pool
	nc       *nats.Conn
	subject  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dbURL := pgtest.NewDatabase(t)
	discard := slog.New(slog.DiscardHandler)
	if err := ingest.Migrate(t.Context(), dbURL, discard); err != nil {
		t.Fatalf("migrate ingest: %v", err)
	}
	if err := delivery.Migrate(t.Context(), dbURL, discard); err != nil {
		t.Fatalf("migrate delivery: %v", err)
	}
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return &env{
		ingest:   ingest.NewStore(pool),
		delivery: delivery.NewStore(pool),
		pool:     pool,
		nc:       natstest.Connect(t),
		subject:  natstest.Subject(t),
	}
}

// dispatcher subscribes a dispatcher's intake to subject, as one of the
// dispatchers' queue group, until t finishes.
func (e *env) dispatcher(t *testing.T, subject string) {
	t.Helper()
	nc := natstest.Connect(t)
	in := delivery.NewIntake(e.delivery, func() time.Time { return stored }, slog.New(slog.DiscardHandler))
	sub, err := in.Subscribe(context.WithoutCancel(t.Context()), nc, subject, "dispatchers")
	if err != nil {
		t.Fatalf("subscribe intake: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
}

// respond answers the Batches sent to subject with the reply answer returns,
// until t finishes or the subscription it returns is unsubscribed. It stands
// in for a dispatcher that misbehaves.
func (e *env) respond(t *testing.T, subject string, answer func(*handoffv1.Batch) *handoffv1.BatchReply) *nats.Subscription {
	t.Helper()
	nc := natstest.Connect(t)
	sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
		var batch handoffv1.Batch
		if err := proto.Unmarshal(m.Data, &batch); err != nil {
			t.Errorf("respond: decode batch: %v", err)
			return
		}
		reply := answer(&batch)
		if reply == nil {
			return // No reply, as if it was lost.
		}
		data, err := proto.Marshal(reply)
		if err != nil {
			t.Errorf("respond: encode reply: %v", err)
			return
		}
		if err := m.Respond(data); err != nil {
			t.Errorf("respond: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return sub
}

// forward sends batch to subject, as the relay would, and returns the reply.
func (e *env) forward(t *testing.T, subject string, batch *handoffv1.Batch) *handoffv1.BatchReply {
	t.Helper()
	data, err := proto.Marshal(batch)
	if err != nil {
		t.Errorf("forward: encode batch: %v", err)
		return nil
	}
	resp, err := e.nc.Request(subject, data, 10*time.Second)
	if err != nil {
		t.Errorf("forward: %v", err)
		return nil
	}
	var reply handoffv1.BatchReply
	if err := proto.Unmarshal(resp.Data, &reply); err != nil {
		t.Errorf("forward: decode reply: %v", err)
		return nil
	}
	return &reply
}

// storedDeliveryCount counts the Deliveries in the delivery schema. Whether a
// resend adds rows is a property of storage that delivery.Store deliberately
// doesn't show, so this is a fixed, read-only SELECT rather than a query.
func (e *env) storedDeliveryCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), "SELECT count(*) FROM delivery.deliveries").Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

// relay returns a relay that sends Batches to the test's subject. Fields of
// cfg left zero get the relay's defaults.
func (e *env) relay(t *testing.T, cfg outbox.Config) *outbox.Relay {
	t.Helper()
	cfg.Subject = e.subject
	return outbox.NewRelay(e.ingest, e.nc, cfg, slog.New(slog.DiscardHandler))
}

// storedDeliveries returns msg's Deliveries as the dispatcher stored them,
// in msg's order, with their IDs cleared so they compare with literals.
func (e *env) storedDeliveries(t *testing.T, msg ingest.Message) []delivery.Delivery {
	t.Helper()
	var got []delivery.Delivery
	for _, d := range msg.Deliveries {
		stored, err := e.delivery.Delivery(t.Context(), d.ID)
		if err != nil {
			t.Errorf("Delivery(%s): %v", d.ID, err)
			continue
		}
		stored.ID = uuid.UUID{}
		got = append(got, stored)
	}
	return got
}

// app is an Application of a Tenant's of its own.
type app struct {
	store    *ingest.Store
	tenantID uuid.UUID
	id       uuid.UUID
}

func (e *env) application(t *testing.T) app {
	t.Helper()
	tenant, err := e.ingest.CreateTenant(t.Context(), "Acme")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	a, err := e.ingest.CreateApplication(t.Context(), tenant.ID, "Billing")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	return app{store: e.ingest, tenantID: tenant.ID, id: a.ID}
}

// endpoint creates an Endpoint of the Application, with the signing secret
// secretFor(its ID), and returns its ID.
func (a app) endpoint(t *testing.T, url string, filter []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := a.store.CreateEndpoint(t.Context(), ingest.NewEndpoint{
		ID:                      id,
		TenantID:                a.tenantID,
		ApplicationID:           a.id,
		URL:                     url,
		Filter:                  filter,
		SigningSecretCiphertext: secretFor(id),
	})
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	return id
}

// message accepts a Message for the Application.
func (a app) message(t *testing.T, eventType string, payload []byte) ingest.Message {
	t.Helper()
	et, err := eventtype.ParseEventType(eventType)
	if err != nil {
		t.Fatalf("parse event type: %v", err)
	}
	msg, _, err := a.store.CreateMessage(t.Context(), ingest.NewMessage{
		TenantID:      a.tenantID,
		ApplicationID: a.id,
		EventType:     et,
		Payload:       payload,
	}, accepted)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	return msg
}

// secretFor returns the stand-in for an Endpoint's encrypted signing secret.
// The relay carries it as opaque bytes, so it needn't be real ciphertext.
func secretFor(endpointID uuid.UUID) []byte {
	return []byte("ciphertext-" + endpointID.String())
}
