package tenantapi

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/madskrogh/herald/internal/eventtype"
	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
)

// messageServer implements MessageService.
type messageServer struct {
	tenantv1.UnimplementedMessageServiceServer
	store *ingest.Store
}

func (s *messageServer) CreateMessage(ctx context.Context, req *tenantv1.CreateMessageRequest) (*tenantv1.CreateMessageResponse, error) {
	applicationID, err := parseID("Application", req.GetApplicationId())
	if err != nil {
		return nil, err
	}
	et, err := eventtype.ParseEventType(req.GetEventType())
	if err != nil {
		return nil, invalidArgument(err)
	}
	if err := checkPayload(req.GetPayload()); err != nil {
		return nil, err
	}
	key, err := idempotencyKey(ctx)
	if err != nil {
		return nil, err
	}
	msg, _, err := s.store.CreateMessage(ctx, ingest.NewMessage{
		TenantID:       tenantID(ctx),
		ApplicationID:  applicationID,
		EventType:      et,
		Payload:        []byte(req.GetPayload()),
		IdempotencyKey: key,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	return &tenantv1.CreateMessageResponse{Message: toMessage(msg)}, nil
}

// idempotencyKeyMetadata is the gRPC metadata key of a call's
// Idempotency-Key. The REST gateway forwards the Idempotency-Key header as it.
const idempotencyKeyMetadata = "idempotency-key"

// idempotencyKey returns the call's Idempotency-Key, or empty if it has none.
func idempotencyKey(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, idempotencyKeyMetadata)
	switch {
	case len(values) == 0:
		return "", nil
	case len(values) > 1:
		return "", invalidArgument(errors.New("want at most one Idempotency-Key"))
	}
	if err := checkIdempotencyKey(values[0]); err != nil {
		return "", invalidArgument(err)
	}
	return values[0], nil
}

func toMessage(msg ingest.Message) *tenantv1.Message {
	return &tenantv1.Message{
		Id:            msg.ID.String(),
		ApplicationId: msg.ApplicationID.String(),
		EventType:     msg.EventType,
		Payload:       string(msg.Payload),
		CreatedAt:     timestamppb.New(msg.CreatedAt),
	}
}
