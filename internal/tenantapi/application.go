package tenantapi

import (
	"context"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
)

// applicationServer implements ApplicationService.
type applicationServer struct {
	tenantv1.UnimplementedApplicationServiceServer
	store *ingest.Store
}

func (s *applicationServer) CreateApplication(ctx context.Context, req *tenantv1.CreateApplicationRequest) (*tenantv1.CreateApplicationResponse, error) {
	app, err := s.store.CreateApplication(ctx, tenantID(ctx), req.GetName())
	if err != nil {
		return nil, err
	}
	return &tenantv1.CreateApplicationResponse{Application: toApplication(app)}, nil
}

func (s *applicationServer) GetApplication(ctx context.Context, req *tenantv1.GetApplicationRequest) (*tenantv1.GetApplicationResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid Application ID %q", req.GetId())
	}
	app, err := s.store.Application(ctx, tenantID(ctx), id)
	if err != nil {
		return nil, err
	}
	return &tenantv1.GetApplicationResponse{Application: toApplication(app)}, nil
}

func toApplication(app ingest.Application) *tenantv1.Application {
	return &tenantv1.Application{
		Id:        app.ID.String(),
		Name:      app.Name,
		CreatedAt: timestamppb.New(app.CreatedAt),
	}
}
