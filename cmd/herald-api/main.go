// Command herald-api is Herald's Tenant-facing service. It authenticates
// Tenants, manages Applications and Endpoints, accepts Messages and hands
// their Deliveries off to herald-dispatcher.
//
// Usage:
//
//	herald-api            run the service
//	herald-api migrate    apply the ingest schema's migrations and exit
//
// Configuration, from the environment:
//
//	HERALD_DATABASE_URL   Postgres connection URL (required)
//	HERALD_KEYRING        keys that encrypt signing secrets, as described in
//	                      internal/keyring (required to run the service)
//	HERALD_NATS_URL       NATS server URL, for handing Deliveries off to
//	                      herald-dispatcher (required to run the service)
//	HERALD_GRPC_ADDR      address the Tenant API serves gRPC on (default :9090)
//	HERALD_HTTP_ADDR      address the Tenant API serves REST on (default :8080)
//	HERALD_ALLOW_INSECURE_ENDPOINTS
//	                      "true" accepts Endpoint URLs with the http scheme,
//	                      not only https; for the local stack and tests only
//	                      (default false)
//	HERALD_PPROF_ADDR     internal address net/http/pprof is served on; never
//	                      a Tenant-facing one (default: pprof not served)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/madskrogh/herald/internal/config"
	"github.com/madskrogh/herald/internal/handoff"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/outbox"
	"github.com/madskrogh/herald/internal/profiling"
	"github.com/madskrogh/herald/internal/tenantapi"
)

// shutdownTimeout is how long calls in progress get to finish once shutdown
// starts. It stays under the 10 seconds Docker and Kubernetes wait before
// killing a container.
const shutdownTimeout = 8 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	// Once shutdown starts, restore default signal handling so a second
	// signal kills a shutdown that hangs.
	context.AfterFunc(ctx, stop)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "herald-api:", err)
		os.Exit(1)
	}
}

// run executes the command in args, logging JSON to stdout. With no command
// it starts the service and blocks until ctx is cancelled. Configuration is
// read through getenv.
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(stdout, nil)).With("service", "herald-api")

	if len(args) == 0 {
		cfg, err := loadSettings(getenv)
		if err != nil {
			return err
		}
		return serve(ctx, cfg, logger)
	}
	switch args[0] {
	case "migrate":
		if len(args) > 1 {
			return fmt.Errorf("migrate: unexpected argument %q", args[1])
		}
		dbURL, err := config.DatabaseURL(getenv)
		if err != nil {
			return err
		}
		return ingest.Migrate(ctx, dbURL, logger)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// settings is the service's configuration: the settings shared with the
// other service, and its own.
type settings struct {
	shared   config.Shared
	grpcAddr string
	httpAddr string
}

func loadSettings(getenv func(string) string) (settings, error) {
	shared, err := config.Load(getenv)
	if err != nil {
		return settings{}, err
	}
	cfg := settings{
		shared:   shared,
		grpcAddr: getenv("HERALD_GRPC_ADDR"),
		httpAddr: getenv("HERALD_HTTP_ADDR"),
	}
	if cfg.grpcAddr == "" {
		cfg.grpcAddr = ":9090"
	}
	if cfg.httpAddr == "" {
		cfg.httpAddr = ":8080"
	}
	return cfg, nil
}

// serve runs the Tenant API over gRPC and REST until ctx is cancelled or a
// server fails, then shuts both down, letting calls in progress finish.
func serve(ctx context.Context, cfg settings, logger *slog.Logger) error {
	// First, before anything else can log.
	if cfg.shared.AllowInsecureEndpoints {
		logger.WarnContext(ctx, "insecure Endpoints allowed: for local development only")
	}
	pool, err := pgxpool.New(ctx, cfg.shared.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()
	store := ingest.NewStore(pool)

	nc, err := nats.Connect(cfg.shared.NATSURL,
		nats.Name("herald-api"),
		// Start without NATS, and keep reconnecting however long it is away:
		// accepting Messages doesn't need it (ADR 0002). Meanwhile the
		// relay's rounds fail, and Messages wait in the outbox.
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	defer nc.Close()

	var lc net.ListenConfig
	var started []any // The "started" log line's attributes.
	if cfg.shared.PprofAddr != "" {
		pprofLis, err := lc.Listen(ctx, "tcp", cfg.shared.PprofAddr)
		if err != nil {
			return fmt.Errorf("listen for pprof: %w", err)
		}
		defer profiling.Start(ctx, pprofLis, logger)()
		started = append(started, "pprof_addr", pprofLis.Addr().String())
	}
	grpcLis, err := lc.Listen(ctx, "tcp", cfg.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	httpLis, err := lc.Listen(ctx, "tcp", cfg.httpAddr)
	if err != nil {
		grpcLis.Close()
		return fmt.Errorf("listen for REST: %w", err)
	}

	grpcServer := tenantapi.NewGRPCServer(store, cfg.shared.Keyring, tenantapi.Config{
		AllowHTTPEndpoints: cfg.shared.AllowInsecureEndpoints,
	}, logger)
	// The gateway calls the gRPC server like any other client, so REST calls
	// pass through the same interceptors.
	conn, err := grpc.NewClient(loopback(grpcLis.Addr()), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		grpcLis.Close()
		httpLis.Close()
		return fmt.Errorf("connect gateway to gRPC: %w", err)
	}
	defer conn.Close()
	gateway, err := tenantapi.NewGateway(ctx, conn)
	if err != nil {
		grpcLis.Close()
		httpLis.Close()
		return err
	}
	httpServer := &http.Server{
		Handler:           gateway,
		ReadHeaderTimeout: 10 * time.Second,
		// Bounds how long a slow client can hold a connection while sending
		// a request, and how long an idle one stays open.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
	}

	errc := make(chan error, 2)
	go func() { errc <- grpcServer.Serve(grpcLis) }()
	go func() { errc <- httpServer.Serve(httpLis) }()
	started = append(started, "grpc_addr", grpcLis.Addr().String(), "http_addr", httpLis.Addr().String())
	logger.InfoContext(ctx, "started", started...)

	// One relay per process (ADR 0002): replicas each run one, and claim
	// different Messages.
	relay := outbox.NewRelay(store, nc, outbox.Config{Subject: handoff.Subject}, logger)
	relayCtx, stopRelay := context.WithCancel(ctx)
	relayDone := make(chan struct{})
	go func() {
		relay.Run(relayCtx)
		close(relayDone)
	}()

	// Both servers run until shutdown, so anything they return before it is a
	// failure.
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}

	// A round in progress is rolled back, and its Messages wait in the outbox
	// for the next relay to run.
	stopRelay()
	<-relayDone

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	// REST first: its calls go through the gRPC server.
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	stopGracefully(shutdownCtx, grpcServer)
	logger.InfoContext(ctx, "stopped")
	return errors.Join(serveErr, shutdownErr)
}

// stopGracefully lets calls in progress on srv finish, and stops srv anyway
// once ctx is done.
func stopGracefully(ctx context.Context, srv *grpc.Server) {
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		srv.Stop()
		<-done
	}
}

// loopback returns the address to dial for the listener at addr from the same
// host: an unspecified IP, from listening on ":9090", becomes localhost.
func loopback(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.IsUnspecified() {
		return addr.String()
	}
	return net.JoinHostPort("localhost", fmt.Sprint(tcp.Port))
}
