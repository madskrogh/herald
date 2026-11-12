package tenantapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	tenantv1 "github.com/madskrogh/herald/internal/gen/herald/tenant/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/ingest/ingesttest"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/pgtest"
	"github.com/madskrogh/herald/internal/tenantapi"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// testKeyring is a keyring with one key of 32 zero bytes, under the ID "test".
var testKeyring = "test:" + base64.StdEncoding.EncodeToString(make([]byte, 32))

// api is a Tenant API served over gRPC and REST on loopback, with a database
// of its own.
type api struct {
	store   *ingest.Store
	pool    *pgxpool.Pool
	keyring *keyring.Keyring
	conn    *grpc.ClientConn
	restURL string
}

// newAPI starts a Tenant API for t. Everything it starts stops when t
// finishes.
func newAPI(t *testing.T) *api {
	t.Helper()
	store, pool := ingesttest.NewStore(t, ingesttest.NewDatabase(t))
	kr, err := keyring.Parse(testKeyring)
	if err != nil {
		t.Fatalf("parse keyring: %v", err)
	}

	srv := tenantapi.NewGRPCServer(store, kr, slog.New(slog.DiscardHandler))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }() // Returns nil once Stop runs.
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	gateway, err := tenantapi.NewGateway(t.Context(), conn)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	rest := httptest.NewServer(gateway)
	t.Cleanup(rest.Close)

	return &api{store: store, pool: pool, keyring: kr, conn: conn, restURL: rest.URL}
}

// newTenant creates a Tenant and returns an API key for it.
func (a *api) newTenant(t *testing.T, name string) string {
	t.Helper()
	tenant, err := a.store.CreateTenant(t.Context(), name)
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	key, err := a.store.CreateAPIKey(t.Context(), tenant.ID)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return key
}

// call is one Tenant API operation, written out for both transports, so that
// the tests pin the REST mapping as well as the gRPC method.
type call struct {
	grpcMethod string
	req        proto.Message
	httpMethod string
	path       string // REST path, with any query
	body       string // REST JSON body; empty for none
}

func createApplication(name string) call {
	return call{
		grpcMethod: "/herald.tenant.v1.ApplicationService/CreateApplication",
		req:        &tenantv1.CreateApplicationRequest{Name: name},
		httpMethod: http.MethodPost,
		path:       "/v1/applications",
		body:       `{"name":` + jsonString(name) + `}`,
	}
}

func getApplication(id string) call {
	return call{
		grpcMethod: "/herald.tenant.v1.ApplicationService/GetApplication",
		req:        &tenantv1.GetApplicationRequest{Id: id},
		httpMethod: http.MethodGet,
		path:       "/v1/applications/" + id,
	}
}

// jsonString returns s as a JSON string literal.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err) // A string always marshals.
	}
	return string(b)
}

// transport makes calls to the Tenant API over one protocol.
type transport struct {
	name string
	// invoke makes c with authorization as its "authorization" metadata or
	// HTTP header (none if empty), decodes a successful response into resp
	// unless resp is nil, and returns the call's gRPC status code.
	invoke func(t *testing.T, a *api, authorization string, c call, resp proto.Message) codes.Code
}

var transports = []transport{
	{name: "gRPC", invoke: invokeGRPC},
	{name: "REST", invoke: invokeREST},
}

func invokeGRPC(t *testing.T, a *api, authorization string, c call, resp proto.Message) codes.Code {
	t.Helper()
	ctx := t.Context()
	if authorization != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", authorization)
	}
	if resp == nil {
		// Any response decodes into Empty, as unknown fields.
		resp = &emptypb.Empty{}
	}
	err := a.conn.Invoke(ctx, c.grpcMethod, c.req, resp)
	return status.Code(err)
}

// restCodes maps the HTTP status codes the REST gateway answers with to the
// gRPC codes they stand for. An HTTP status not listed here fails the test.
var restCodes = map[int]codes.Code{
	http.StatusOK:           codes.OK,
	http.StatusBadRequest:   codes.InvalidArgument,
	http.StatusUnauthorized: codes.Unauthenticated,
	http.StatusNotFound:     codes.NotFound,
}

func invokeREST(t *testing.T, a *api, authorization string, c call, resp proto.Message) codes.Code {
	t.Helper()
	code, body := doREST(t, a, authorization, c)
	if code == codes.OK && resp != nil {
		if err := protojson.Unmarshal(body, resp); err != nil {
			t.Fatalf("%s %s: decode response %s: %v", c.httpMethod, c.path, body, err)
		}
	}
	return code
}

// doREST makes c over REST and returns its gRPC code and the response body.
func doREST(t *testing.T, a *api, authorization string, c call) (codes.Code, []byte) {
	t.Helper()
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	req, err := http.NewRequestWithContext(t.Context(), c.httpMethod, a.restURL+c.path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", c.httpMethod, c.path, err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatalf("%s %s: read response: %v", c.httpMethod, c.path, err)
	}
	code, ok := restCodes[res.StatusCode]
	if !ok {
		t.Fatalf("%s %s: unexpected HTTP status %d: %s", c.httpMethod, c.path, res.StatusCode, &buf)
	}
	return code, buf.Bytes()
}

// mustInvoke makes c with an API key and fails the test unless it succeeds.
func (tr transport) mustInvoke(t *testing.T, a *api, key string, c call, resp proto.Message) {
	t.Helper()
	if code := tr.invoke(t, a, "Bearer "+key, c, resp); code != codes.OK {
		t.Fatalf("%s: %s, want OK", c.grpcMethod, code)
	}
}
