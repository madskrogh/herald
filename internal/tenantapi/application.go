package tenantapi

import (
	"context"
	"uuid"

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
	if err := checkApplicationName(req.GetName()); err != nil {
		return nil, invalidArgument(err)
	}
	app, err := s.store.CreateApplication(ctx, tenantID(ctx), req.GetName())
	if err != nil {
		return nil, err
	}
	return &tenantv1.CreateApplicationResponse{Application: toApplication(app)}, nil
}

func (s *applicationServer) GetApplication(ctx context.Context, req *tenantv1.GetApplicationRequest) (*tenantv1.GetApplicationResponse, error) {
	id, err := parseID("Application", req.GetId())
	if err != nil {
		return nil, err
	}
	app, err := s.store.Application(ctx, tenantID(ctx), id)
	if err != nil {
		return nil, err
	}
	return &tenantv1.GetApplicationResponse{Application: toApplication(app)}, nil
}

func (s *applicationServer) ListApplications(ctx context.Context, req *tenantv1.ListApplicationsRequest) (*tenantv1.ListApplicationsResponse, error) {
	p, err := parsePage(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	apps, err := s.store.ListApplications(ctx, tenantID(ctx), p.after, p.fetch())
	if err != nil {
		return nil, err
	}
	apps, next := cut(p, apps, func(app ingest.Application) uuid.UUID { return app.ID })
	resp := &tenantv1.ListApplicationsResponse{NextPageToken: next}
	for _, app := range apps {
		resp.Applications = append(resp.Applications, toApplication(app))
	}
	return resp, nil
}

func toApplication(app ingest.Application) *tenantv1.Application {
	return &tenantv1.Application{
		Id:        app.ID.String(),
		Name:      app.Name,
		CreatedAt: timestamppb.New(app.CreatedAt),
	}
}
