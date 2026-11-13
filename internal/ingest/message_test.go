package ingest_test

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/eventtype"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/ingest/ingestdb"
	"github.com/madskrogh/herald/internal/ingest/ingesttest"
)

// accepted is when the tests' Messages are accepted, unless a test says
// otherwise.
var accepted = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

func TestMessageFansOutToMatchingEndpoints(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	f := newFixture(t, store)
	all := f.endpoint(t, nil)
	invoices := f.endpoint(t, []string{"invoice.*"})
	f.endpoint(t, []string{"customer.>"})
	paidOnly := f.endpoint(t, []string{"customer.created", "invoice.paid"})
	// Another Application's Endpoint, with a filter that matches.
	newFixture(t, store).endpoint(t, nil)

	msg, created, err := store.CreateMessage(t.Context(), f.message("invoice.paid", ""), accepted)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if !created {
		t.Error("CreateMessage reports the Message as a repeat, want created")
	}
	want := []uuid.UUID{all, invoices, paidOnly}
	if diff := cmp.Diff(want, endpointIDs(msg.Deliveries)); diff != "" {
		t.Errorf("Deliveries' Endpoints (-want +got):\n%s", diff)
	}
	if !msg.HandedOffAt.IsZero() {
		t.Errorf("HandedOffAt = %v, want zero: the Deliveries wait for the relay", msg.HandedOffAt)
	}
}

func TestMessageWithoutDeliveriesIsHandedOffAlready(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))

	tests := []struct {
		name    string
		filters [][]string // one Endpoint per filter
	}{
		{name: "no Endpoints"},
		{name: "no matching Endpoints", filters: [][]string{{"customer.>"}, {"invoice.created"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, store)
			for _, filter := range tt.filters {
				f.endpoint(t, filter)
			}

			msg, _, err := store.CreateMessage(t.Context(), f.message("invoice.paid", ""), accepted)
			if err != nil {
				t.Fatalf("CreateMessage: %v", err)
			}
			if len(msg.Deliveries) != 0 {
				t.Errorf("Deliveries = %v, want none", msg.Deliveries)
			}
			if !msg.HandedOffAt.Equal(accepted) {
				t.Errorf("HandedOffAt = %v, want %v: there is nothing to hand off", msg.HandedOffAt, accepted)
			}
		})
	}
}

func TestMessageForAnotherTenantsApplicationIsNotFound(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	owner := newFixture(t, store)
	owner.endpoint(t, nil)
	other := newFixture(t, store)

	tests := []struct {
		name          string
		applicationID uuid.UUID
	}{
		{name: "another Tenant's Application", applicationID: owner.application.ID},
		{name: "Application that doesn't exist", applicationID: uuid.MustParse("0193a1b2-0000-7000-8000-000000000000")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := other.message("invoice.paid", "")
			m.ApplicationID = tt.applicationID
			if msg, _, err := store.CreateMessage(t.Context(), m, accepted); !errors.Is(err, ingest.ErrNotFound) {
				t.Errorf("CreateMessage = Message %s, error %v; want ingest.ErrNotFound", msg.ID, err)
			}
		})
	}

	// Positive control: the owner can create the same Message.
	if _, _, err := store.CreateMessage(t.Context(), owner.message("invoice.paid", ""), accepted); err != nil {
		t.Errorf("owner's CreateMessage: %v", err)
	}
}

func TestRepeatWithIdempotencyKeyReturnsTheOriginalMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, pool := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	f := newFixture(t, store)
	f.endpoint(t, nil)

	original, _, err := store.CreateMessage(t.Context(), f.message("invoice.paid", "order-1"), accepted)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	// A repeat that worked out Deliveries afresh would add one to this
	// Endpoint.
	f.endpoint(t, nil)
	before := countMessagesAndDeliveries(t, pool)

	repeat, created, err := store.CreateMessage(t.Context(), f.message("invoice.paid", "order-1"), accepted.Add(time.Minute))
	if err != nil {
		t.Fatalf("repeat CreateMessage: %v", err)
	}
	if created {
		t.Error("repeat CreateMessage reports the Message as created, want a repeat")
	}
	if diff := cmp.Diff(original, repeat); diff != "" {
		t.Errorf("repeat returned another Message (-original +repeat):\n%s", diff)
	}
	if after := countMessagesAndDeliveries(t, pool); after != before {
		t.Errorf("rows after the repeat = %+v, want %+v as before it", after, before)
	}

	// Positive control: without a key, the same check sees a second Message
	// and its Deliveries.
	if _, _, err := store.CreateMessage(t.Context(), f.message("invoice.paid", ""), accepted); err != nil {
		t.Fatalf("CreateMessage without a key: %v", err)
	}
	want := rowCounts{Messages: before.Messages + 1, Deliveries: before.Deliveries + 2}
	if got := countMessagesAndDeliveries(t, pool); got != want {
		t.Fatalf("rows after a Message without a key = %+v, want %+v", got, want)
	}
}

func TestRepeatWithADifferentRequestIsRefused(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, pool := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	f := newFixture(t, store)
	f.endpoint(t, nil)
	other, err := store.CreateApplication(t.Context(), f.tenant.ID, "Customer B")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if _, _, err := store.CreateMessage(t.Context(), f.message("invoice.paid", "order-1"), accepted); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	before := countMessagesAndDeliveries(t, pool)

	// Each differs from the original in one way. The original's payload is
	// {"invoice_id": "in_1"}.
	tests := []struct {
		name   string
		change func(m *ingest.NewMessage)
	}{
		{name: "another Application", change: func(m *ingest.NewMessage) { m.ApplicationID = other.ID }},
		{name: "another Event type", change: func(m *ingest.NewMessage) { m.EventType = mustParseEventType("invoice.created") }},
		{name: "a payload one byte apart", change: func(m *ingest.NewMessage) { m.Payload = []byte(`{"invoice_id": "in_2"}`) }},
		// The same JSON, written differently: the comparison is byte for byte.
		{name: "a payload spaced differently", change: func(m *ingest.NewMessage) { m.Payload = []byte(`{"invoice_id":"in_1"}`) }},
	}
	// Not parallel: the positive control below takes the key over.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := f.message("invoice.paid", "order-1")
			tt.change(&m)
			msg, _, err := store.CreateMessage(t.Context(), m, accepted.Add(time.Minute))
			if !errors.Is(err, ingest.ErrIdempotencyKeyReused) {
				t.Errorf("CreateMessage = Message %s, error %v; want ingest.ErrIdempotencyKeyReused", msg.ID, err)
			}
		})
	}
	if after := countMessagesAndDeliveries(t, pool); after != before {
		t.Errorf("rows after the refused repeats = %+v, want %+v as before them", after, before)
	}

	// Positive control: once the key has expired, a different request takes
	// it over.
	t.Run("after the key expired", func(t *testing.T) {
		m := f.message("invoice.created", "order-1")
		if _, created, err := store.CreateMessage(t.Context(), m, accepted.Add(24*time.Hour)); err != nil || !created {
			t.Errorf("CreateMessage after 24 h: created %t, error %v; want a new Message", created, err)
		}
	})
}

func TestIdempotencyKeyExpiresAfter24Hours(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	f := newFixture(t, store)
	f.endpoint(t, nil)
	create := func(t *testing.T, at time.Time) (ingest.Message, bool) {
		t.Helper()
		msg, created, err := store.CreateMessage(t.Context(), f.message("invoice.paid", "order-1"), at)
		if err != nil {
			t.Fatalf("CreateMessage at %v: %v", at, err)
		}
		return msg, created
	}

	original, _ := create(t, accepted)
	// Postgres stores microseconds, so this is the last moment the key holds.
	if repeat, created := create(t, accepted.Add(24*time.Hour-time.Microsecond)); created || repeat.ID != original.ID {
		t.Errorf("CreateMessage just before 24 h = Message %s (created %t), want the original %s", repeat.ID, created, original.ID)
	}

	reused, created := create(t, accepted.Add(24*time.Hour))
	if !created || reused.ID == original.ID {
		t.Fatalf("CreateMessage at 24 h = Message %s (created %t), want a new Message", reused.ID, created)
	}
	// The key now holds the new Message.
	if repeat, created := create(t, accepted.Add(25*time.Hour)); created || repeat.ID != reused.ID {
		t.Errorf("CreateMessage after reuse = Message %s (created %t), want %s", repeat.ID, created, reused.ID)
	}
}

func TestIdempotencyKeysArePerTenant(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	store, _ := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	acme, globex := newFixture(t, store), newFixture(t, store)

	first, _, err := store.CreateMessage(t.Context(), acme.message("invoice.paid", "order-1"), accepted)
	if err != nil {
		t.Fatalf("Acme's CreateMessage: %v", err)
	}
	second, created, err := store.CreateMessage(t.Context(), globex.message("invoice.paid", "order-1"), accepted)
	if err != nil {
		t.Fatalf("Globex's CreateMessage: %v", err)
	}
	if !created || second.ID == first.ID || second.TenantID != globex.tenant.ID {
		t.Errorf("Globex's Message %s of Tenant %s (created %t), want a new Message of its own", second.ID, second.TenantID, created)
	}
}

func TestConcurrentRequestsWithTheSameKeyCreateOneMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}

	tests := []struct {
		name string
		// expired is whether a Message accepted 24 hours earlier holds the key,
		// so that every request tries to take it over.
		expired bool
	}{
		{name: "new key"},
		{name: "expired key", expired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, pool := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
			f := newFixture(t, store)
			f.endpoint(t, nil)
			var holder ingest.Message
			if tt.expired {
				var err error
				holder, _, err = store.CreateMessage(t.Context(), f.message("invoice.paid", "order-1"), accepted.Add(-24*time.Hour))
				if err != nil {
					t.Fatalf("CreateMessage 24 h earlier: %v", err)
				}
			}
			before := countMessagesAndDeliveries(t, pool)

			type result struct {
				msg     ingest.Message
				created bool
				err     error
			}
			results := make([]result, 16)
			concurrently(t, pool, len(results), func(i int) {
				msg, created, err := store.CreateMessage(t.Context(), f.message("invoice.paid", "order-1"), accepted)
				results[i] = result{msg: msg, created: created, err: err}
			})

			var created int
			for i, r := range results {
				if r.err != nil {
					t.Fatalf("request %d: %v", i, r.err)
				}
				if r.created {
					created++
				}
				if diff := cmp.Diff(results[0].msg, r.msg); diff != "" {
					t.Errorf("request %d got another Message than request 0 (-0 +%d):\n%s", i, i, diff)
				}
			}
			if created != 1 {
				t.Errorf("%d requests report a created Message, want 1", created)
			}
			if results[0].msg.ID == holder.ID {
				t.Errorf("requests got the Message %s that held the expired key, want a new one", holder.ID)
			}
			want := rowCounts{Messages: before.Messages + 1, Deliveries: before.Deliveries + 1}
			if got := countMessagesAndDeliveries(t, pool); got != want {
				t.Errorf("rows = %+v, want %+v", got, want)
			}
		})
	}
}

// rowCounts is how many Messages and Deliveries a database holds.
type rowCounts struct {
	Messages, Deliveries int
}

// countMessagesAndDeliveries counts the rows in the messages and deliveries
// tables. The query lives here, not in sqlc: whether a call wrote rows it
// doesn't return is the property under test, and the Store has no way to
// show it.
func countMessagesAndDeliveries(t *testing.T, db ingestdb.DBTX) rowCounts {
	t.Helper()
	var c rowCounts
	err := db.QueryRow(t.Context(),
		"SELECT (SELECT count(*) FROM ingest.messages), (SELECT count(*) FROM ingest.deliveries)",
	).Scan(&c.Messages, &c.Deliveries)
	if err != nil {
		t.Fatalf("count messages and deliveries: %v", err)
	}
	return c
}

// fixture is a Tenant with one Application, for creating Endpoints and
// Messages under.
type fixture struct {
	store       *ingest.Store
	tenant      ingest.Tenant
	application ingest.Application
}

func newFixture(t *testing.T, store *ingest.Store) fixture {
	t.Helper()
	tenant, err := store.CreateTenant(t.Context(), "Acme")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	application, err := store.CreateApplication(t.Context(), tenant.ID, "Customer A")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	return fixture{store: store, tenant: tenant, application: application}
}

// endpoint creates an Endpoint with filter under the fixture's Application
// and returns its ID.
func (f fixture) endpoint(t *testing.T, filter []string) uuid.UUID {
	t.Helper()
	ep, err := f.createEndpoint(t, filter)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	return ep.ID
}

// createEndpoint creates an Endpoint with filter under the fixture's
// Application, and returns what CreateEndpoint returns.
func (f fixture) createEndpoint(t *testing.T, filter []string) (ingest.Endpoint, error) {
	t.Helper()
	return f.store.CreateEndpoint(t.Context(), ingest.NewEndpoint{
		ID:                      uuid.NewV7(),
		TenantID:                f.tenant.ID,
		ApplicationID:           f.application.ID,
		URL:                     "https://example.com/webhooks",
		Filter:                  filter,
		SigningSecretCiphertext: []byte("ciphertext"),
	})
}

// message returns a Message of the given Event type for the fixture's
// Application, with the Idempotency-Key key (none if empty).
func (f fixture) message(eventType, key string) ingest.NewMessage {
	return ingest.NewMessage{
		TenantID:       f.tenant.ID,
		ApplicationID:  f.application.ID,
		EventType:      mustParseEventType(eventType),
		Payload:        []byte(`{"invoice_id": "in_1"}`),
		IdempotencyKey: key,
	}
}

// mustParseEventType parses one of the tests' Event types, which are literals.
func mustParseEventType(s string) eventtype.EventType {
	et, err := eventtype.ParseEventType(s)
	if err != nil {
		panic(err)
	}
	return et
}

// endpointIDs returns the IDs of the Endpoints the Deliveries go to.
func endpointIDs(deliveries []ingest.Delivery) []uuid.UUID {
	ids := make([]uuid.UUID, len(deliveries))
	for i, d := range deliveries {
		ids[i] = d.EndpointID
	}
	return ids
}
