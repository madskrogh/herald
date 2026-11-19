package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	"github.com/madskrogh/herald/internal/delivery/deliverydb"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/telemetry"
)

// storeTimeout bounds how long storing one Batch may take. Batches are
// handled one at a time, so a hung database call would otherwise hold up
// every Batch after it, and shutdown with it. It stays under the time a
// dispatcher gets to drain its Batches at shutdown.
const storeTimeout = 5 * time.Second

// Intake stores the Batches the relay in herald-api hands off, and answers
// each one with a BatchReply.
type Intake struct {
	store  *Store
	now    func() time.Time
	tracer trace.Tracer
	logger *slog.Logger
}

// NewIntake returns an Intake that stores Batches in store. now tells the
// current time, at which new Deliveries are due, and tp makes the span of
// each Message stored.
func NewIntake(store *Store, now func() time.Time, tp trace.TracerProvider, logger *slog.Logger) *Intake {
	return &Intake{store: store, now: now, tracer: tp.Tracer(tracerName), logger: logger}
}

// Subscribe subscribes the intake to Batches sent to subject, in queue group
// queue, and returns once the NATS server has the subscription.
//
// Batches are handled with ctx's values but not its cancellation: when
// shutdown cancels ctx and drains the connection, the Batches already
// received are still stored and answered.
func (in *Intake) Subscribe(ctx context.Context, nc *nats.Conn, subject, queue string) (*nats.Subscription, error) {
	ctx = context.WithoutCancel(ctx)
	sub, err := nc.QueueSubscribe(subject, queue, func(m *nats.Msg) {
		in.handle(ctx, m)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}
	if err := nc.Flush(); err != nil {
		// The server may never have seen the subscription. Remove it, so a
		// failed Subscribe leaves no handler behind.
		_ = sub.Unsubscribe()
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}
	return sub, nil
}

// handle stores the Batch in m and answers it. The reply is sent only after
// the commit.
func (in *Intake) handle(ctx context.Context, m *nats.Msg) {
	reply := &handoffv1.BatchReply{Committed: true}
	if err := in.storeBatch(ctx, m.Data); err != nil {
		in.logger.WarnContext(ctx, "batch not stored", "error", err)
		reply = &handoffv1.BatchReply{Committed: false, Error: err.Error()}
	}
	data, err := proto.Marshal(reply)
	if err != nil {
		in.logger.ErrorContext(ctx, "encode batch reply", "error", err)
		return
	}
	if err := m.Respond(data); err != nil {
		in.logger.ErrorContext(ctx, "send batch reply", "error", err)
	}
}

// storeBatch stores the Batch encoded in data in one transaction, within
// storeTimeout.
//
// Each Message gets a span, in the Message's trace when the relay sent its
// context, and its Deliveries keep the span's context. Every Attempt for a
// Delivery is then a child of that span, whenever and wherever it runs.
func (in *Intake) storeBatch(ctx context.Context, data []byte) error {
	batch, err := decodeBatch(data, in.now())
	if err != nil {
		return err
	}
	var (
		messages   []deliverydb.InsertMessageParams
		deliveries []deliverydb.InsertDeliveryParams
		spans      []trace.Span
	)
	for _, m := range batch {
		spanCtx, span := telemetry.Start(ctx, in.tracer, m.traceParent, telemetry.SpanStoreDeliveries,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				telemetry.TenantID.String(m.message.TenantID.String()),
				telemetry.ApplicationID.String(applicationID(m.message.ApplicationID)),
				telemetry.MessageID.String(m.message.ID.String()),
				telemetry.BatchSize.Int(len(batch)),
				telemetry.DeliveryCount.Int(len(m.deliveries)),
			),
		)
		spans = append(spans, span)
		traceParent := telemetry.TraceParent(spanCtx)
		for _, d := range m.deliveries {
			d.Traceparent = traceParent
			deliveries = append(deliveries, d)
		}
		messages = append(messages, m.message)
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	err = in.store.insert(ctx, messages, deliveries)
	outcome := "stored"
	if err != nil {
		outcome = "failed"
	}
	for _, span := range spans {
		telemetry.End(span, outcome, err)
	}
	return err
}

// batchMessage is one Message of a Batch, decoded into the rows that store
// it.
type batchMessage struct {
	message    deliverydb.InsertMessageParams
	deliveries []deliverydb.InsertDeliveryParams
	// traceParent is the context of the relay's span for the Message, or
	// empty from a relay that doesn't send it.
	traceParent string
}

// decodeBatch decodes the Batch in data into the rows that store it. Its
// Deliveries are pending and due at now.
func decodeBatch(data []byte, now time.Time) ([]batchMessage, error) {
	var batch handoffv1.Batch
	if err := proto.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("decode batch: %w", err)
	}
	var messages []batchMessage
	for _, m := range batch.GetMessages() {
		message, err := parseMessage(m)
		if err != nil {
			return nil, err
		}
		bm := batchMessage{message: message, traceParent: m.GetTraceparent()}
		for _, d := range m.GetDeliveries() {
			delivery, err := parseDelivery(d, message.ID, now)
			if err != nil {
				return nil, fmt.Errorf("message %s: %w", message.ID, err)
			}
			bm.deliveries = append(bm.deliveries, delivery)
		}
		messages = append(messages, bm)
	}
	return messages, nil
}

// applicationID returns id as a span attribute: empty for a Message handed
// off before Batches carried its Application.
func applicationID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func parseMessage(m *handoffv1.Message) (deliverydb.InsertMessageParams, error) {
	id, err := uuid.Parse(m.GetId())
	if err != nil {
		return deliverydb.InsertMessageParams{}, fmt.Errorf("message ID %q: %w", m.GetId(), err)
	}
	tenantID, err := uuid.Parse(m.GetTenantId())
	if err != nil {
		return deliverydb.InsertMessageParams{}, fmt.Errorf("message %s: tenant ID %q: %w", id, m.GetTenantId(), err)
	}
	// A relay that predates application_id sends none.
	var applicationID pgtype.UUID
	if s := m.GetApplicationId(); s != "" {
		parsed, err := uuid.Parse(s)
		if err != nil {
			return deliverydb.InsertMessageParams{}, fmt.Errorf("message %s: application ID %q: %w", id, s, err)
		}
		applicationID = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	return deliverydb.InsertMessageParams{
		ID:            id,
		TenantID:      tenantID,
		ApplicationID: applicationID,
		EventType:     m.GetEventType(),
		Payload:       m.GetPayload(),
	}, nil
}

func parseDelivery(d *handoffv1.Delivery, messageID uuid.UUID, now time.Time) (deliverydb.InsertDeliveryParams, error) {
	id, err := uuid.Parse(d.GetId())
	if err != nil {
		return deliverydb.InsertDeliveryParams{}, fmt.Errorf("delivery ID %q: %w", d.GetId(), err)
	}
	endpoint := d.GetEndpoint()
	endpointID, err := uuid.Parse(endpoint.GetId())
	if err != nil {
		return deliverydb.InsertDeliveryParams{}, fmt.Errorf("delivery %s: endpoint ID %q: %w", id, endpoint.GetId(), err)
	}
	secrets := make([][]byte, 0, len(endpoint.GetSigningSecrets()))
	for _, s := range endpoint.GetSigningSecrets() {
		secrets = append(secrets, s.GetCiphertext())
	}
	return deliverydb.InsertDeliveryParams{
		ID:             id,
		MessageID:      messageID,
		EndpointID:     endpointID,
		EndpointURL:    endpoint.GetUrl(),
		SigningSecrets: secrets,
		Now:            now,
	}, nil
}
