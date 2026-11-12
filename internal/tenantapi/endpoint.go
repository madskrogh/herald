package tenantapi

import (
	"context"
	"fmt"
	"uuid"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/madskrogh/herald/internal/eventtype"
	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/signing"
)

// endpointServer implements EndpointService.
type endpointServer struct {
	tenantv1.UnimplementedEndpointServiceServer
	store   *ingest.Store
	keyring *keyring.Keyring
}

func (s *endpointServer) CreateEndpoint(ctx context.Context, req *tenantv1.CreateEndpointRequest) (*tenantv1.CreateEndpointResponse, error) {
	applicationID, err := parseID("Application", req.GetApplicationId())
	if err != nil {
		return nil, err
	}
	if err := checkEndpointURL(req.GetUrl()); err != nil {
		return nil, invalidArgument(err)
	}
	if _, err := eventtype.ParseFilter(req.GetFilter()); err != nil {
		return nil, invalidArgument(err)
	}

	id := uuid.NewV7()
	secret := signing.NewSecret()
	// The ID as associated data binds the ciphertext to this Endpoint, so it
	// fails to decrypt if it is ever copied onto another Endpoint's row.
	ciphertext, err := s.keyring.Encrypt([]byte(secret), []byte(id.String()))
	if err != nil {
		return nil, fmt.Errorf("encrypt signing secret: %w", err)
	}
	ep, err := s.store.CreateEndpoint(ctx, ingest.NewEndpoint{
		ID:                      id,
		TenantID:                tenantID(ctx),
		ApplicationID:           applicationID,
		URL:                     req.GetUrl(),
		Filter:                  req.GetFilter(),
		SigningSecretCiphertext: ciphertext,
	})
	if err != nil {
		return nil, err
	}
	return &tenantv1.CreateEndpointResponse{Endpoint: toEndpoint(ep), SigningSecret: secret}, nil
}

func (s *endpointServer) GetEndpoint(ctx context.Context, req *tenantv1.GetEndpointRequest) (*tenantv1.GetEndpointResponse, error) {
	applicationID, err := parseID("Application", req.GetApplicationId())
	if err != nil {
		return nil, err
	}
	id, err := parseID("Endpoint", req.GetId())
	if err != nil {
		return nil, err
	}
	ep, err := s.store.Endpoint(ctx, tenantID(ctx), applicationID, id)
	if err != nil {
		return nil, err
	}
	return &tenantv1.GetEndpointResponse{Endpoint: toEndpoint(ep)}, nil
}

func (s *endpointServer) ListEndpoints(ctx context.Context, req *tenantv1.ListEndpointsRequest) (*tenantv1.ListEndpointsResponse, error) {
	applicationID, err := parseID("Application", req.GetApplicationId())
	if err != nil {
		return nil, err
	}
	p, err := parsePage(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	eps, err := s.store.ListEndpoints(ctx, tenantID(ctx), applicationID, p.after, p.fetch())
	if err != nil {
		return nil, err
	}
	eps, next := cut(p, eps, func(ep ingest.Endpoint) uuid.UUID { return ep.ID })
	resp := &tenantv1.ListEndpointsResponse{NextPageToken: next}
	for _, ep := range eps {
		resp.Endpoints = append(resp.Endpoints, toEndpoint(ep))
	}
	return resp, nil
}

func toEndpoint(ep ingest.Endpoint) *tenantv1.Endpoint {
	return &tenantv1.Endpoint{
		Id:            ep.ID.String(),
		ApplicationId: ep.ApplicationID.String(),
		Url:           ep.URL,
		Filter:        ep.Filter,
		CreatedAt:     timestamppb.New(ep.CreatedAt),
	}
}
