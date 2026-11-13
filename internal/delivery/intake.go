package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/madskrogh/herald/internal/delivery/deliverydb"
	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
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
	logger *slog.Logger
}

// NewIntake returns an Intake that stores Batches in store. now tells the
// current time, at which new Deliveries are due.
func NewIntake(store *Store, now func() time.Time, logger *slog.Logger) *Intake {
	return &Intake{store: store, now: now, logger: logger}
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

// handle stores the Batch in m in one transaction and answers it. The reply
// is sent only after the commit.
func (in *Intake) handle(ctx context.Context, m *nats.Msg) {
	messages, deliveries, err := decodeBatch(m.Data, in.now())
	if err == nil {
		storeCtx, cancel := context.WithTimeout(ctx, storeTimeout)
		err = in.store.insert(storeCtx, messages, deliveries)
		cancel()
	}
	reply := &handoffv1.BatchReply{Committed: true}
	if err != nil {
		in.logger.WarnContext(ctx, "batch not stored", "error", err)
		reply = &handoffv1.BatchReply{Error: err.Error()}
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

// decodeBatch decodes the Batch in data into the rows that store it. Its
// Deliveries are pending and due at now.
func decodeBatch(data []byte, now time.Time) ([]deliverydb.InsertMessageParams, []deliverydb.InsertDeliveryParams, error) {
	var batch handoffv1.Batch
	if err := proto.Unmarshal(data, &batch); err != nil {
		return nil, nil, fmt.Errorf("decode batch: %w", err)
	}
	var (
		messages   []deliverydb.InsertMessageParams
		deliveries []deliverydb.InsertDeliveryParams
	)
	for _, m := range batch.GetMessages() {
		message, err := parseMessage(m)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, message)
		for _, d := range m.GetDeliveries() {
			delivery, err := parseDelivery(d, message.ID, now)
			if err != nil {
				return nil, nil, fmt.Errorf("message %s: %w", message.ID, err)
			}
			deliveries = append(deliveries, delivery)
		}
	}
	return messages, deliveries, nil
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
	return deliverydb.InsertMessageParams{
		ID:        id,
		TenantID:  tenantID,
		EventType: m.GetEventType(),
		Payload:   m.GetPayload(),
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
