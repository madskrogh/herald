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
func (in *Intake) Subscribe(ctx context.Context, nc *nats.Conn, subject, queue string) (*nats.Subscription, error) {
	sub, err := nc.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		in.handle(ctx, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}
	if err := nc.Flush(); err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", subject, err)
	}
	return sub, nil
}

// handle stores the Batch in msg and answers it.
func (in *Intake) handle(ctx context.Context, msg *nats.Msg) {
	reply := &handoffv1.BatchReply{Committed: true}
	if err := in.store.storeBatch(ctx, msg.Data, in.now()); err != nil {
		in.logger.WarnContext(ctx, "batch not stored", "error", err)
		reply = &handoffv1.BatchReply{Error: err.Error()}
	}
	data, err := proto.Marshal(reply)
	if err != nil {
		in.logger.ErrorContext(ctx, "encode batch reply", "error", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		in.logger.ErrorContext(ctx, "send batch reply", "error", err)
	}
}

// storeBatch stores the Batch encoded in data in one transaction. Its
// Deliveries are pending and due at now.
func (s *Store) storeBatch(ctx context.Context, data []byte, now time.Time) error {
	var batch handoffv1.Batch
	if err := proto.Unmarshal(data, &batch); err != nil {
		return fmt.Errorf("decode batch: %w", err)
	}
	var (
		messages   []deliverydb.InsertMessageParams
		deliveries []deliverydb.InsertDeliveryParams
	)
	for _, m := range batch.GetMessages() {
		msg, err := parseMessage(m)
		if err != nil {
			return err
		}
		messages = append(messages, msg)
		for _, d := range m.GetDeliveries() {
			del, err := parseDelivery(d, msg.ID, now)
			if err != nil {
				return fmt.Errorf("message %s: %w", msg.ID, err)
			}
			deliveries = append(deliveries, del)
		}
	}
	return s.insert(ctx, messages, deliveries)
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
