// Command herald-dispatcher stores the Deliveries handed off by herald-api
// and makes signed Attempts for them, retrying each Delivery on a schedule
// and capping the Attempts in flight per Endpoint.
//
// Usage:
//
//	herald-dispatcher            run the service
//	herald-dispatcher migrate    apply the delivery schema's migrations and exit
//
// Configuration, from the environment:
//
//	HERALD_DATABASE_URL   Postgres connection URL (required)
//	HERALD_KEYRING        keys that decrypt signing secrets, as described in
//	                      internal/keyring; herald-api's keyring (required to
//	                      run the service)
//	HERALD_NATS_URL       NATS server URL (required to run the service)
//	HERALD_ALLOW_INSECURE_ENDPOINTS
//	                      "true" lets Attempts connect to loopback, private
//	                      and other non-public addresses; for the local stack
//	                      and tests only (default false)
//	HERALD_RETRY_SCALE    a factor in (0, 1] that shrinks every delay in the
//	                      retry schedule, so the local stack and end-to-end
//	                      tests run it in seconds (default 1)
//	HERALD_OTLP_URL       the OpenTelemetry Collector's OTLP/HTTP base URL,
//	                      such as http://otelcol:4318; without it, spans are
//	                      not exported
//	HERALD_RUN_ID         identifies the run the spans belong to, as a
//	                      resource attribute (default none)
//	HERALD_BUILD_VERSION  identifies the build, as a resource attribute
//	                      (default dev)
//	HERALD_PPROF_ADDR     internal address net/http/pprof is served on; never
//	                      a Tenant-facing one (default: pprof not served)
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/madskrogh/herald/internal/config"
	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/handoff"
	"github.com/madskrogh/herald/internal/profiling"
	"github.com/madskrogh/herald/internal/retry"
	"github.com/madskrogh/herald/internal/telemetry"
)

// shutdownTimeout is how long the Batches already received get to finish once
// shutdown starts. It stays under the 10 seconds Docker and Kubernetes wait
// before killing a container.
const shutdownTimeout = 8 * time.Second

// attemptDrainTimeout is how long the Attempts in flight get to finish once
// shutdown starts. Those still running are then cancelled and their leases
// released, which takes up to 2 seconds more, so the workers too stop within
// shutdownTimeout.
const attemptDrainTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	// Once shutdown starts, restore default signal handling so a second
	// signal kills a shutdown that hangs.
	context.AfterFunc(ctx, stop)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "herald-dispatcher:", err)
		os.Exit(1)
	}
}

// run executes the command in args, logging JSON to stdout. With no command
// it starts the service and blocks until ctx is cancelled. Configuration is
// read through getenv.
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	logger := slog.New(telemetry.NewLogHandler(slog.NewJSONHandler(stdout, nil))).With("service", "herald-dispatcher")

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
		return delivery.Migrate(ctx, dbURL, logger)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// settings is the service's configuration: the settings shared with the
// other service, and its own.
type settings struct {
	shared config.Shared
	// schedule is the retry schedule, at full length unless HERALD_RETRY_SCALE
	// shrinks it.
	schedule retry.Schedule
	// shrunkSchedule is whether HERALD_RETRY_SCALE shrinks the schedule.
	shrunkSchedule bool
}

func loadSettings(getenv func(string) string) (settings, error) {
	shared, err := config.Load(getenv)
	if err != nil {
		return settings{}, err
	}
	cfg := settings{shared: shared}
	// Scale 1: the retry schedule at full length, as docs/design.md gives it.
	scale := 1.0
	if s := getenv("HERALD_RETRY_SCALE"); s != "" {
		scale, err = strconv.ParseFloat(s, 64)
		if err != nil {
			return settings{}, fmt.Errorf("HERALD_RETRY_SCALE: %w", err)
		}
	}
	cfg.schedule, err = retry.New(scale, rand.Float64)
	if err != nil {
		return settings{}, fmt.Errorf("HERALD_RETRY_SCALE: %w", err)
	}
	cfg.shrunkSchedule = scale < 1
	return cfg, nil
}

// serve stores the Batches handed off by herald-api and makes their
// Attempts until ctx is cancelled. Then it lets the Batches already received
// and the Attempts in flight finish.
func serve(ctx context.Context, cfg settings, logger *slog.Logger) error {
	// First, before anything else can log.
	if cfg.shared.AllowInsecureEndpoints {
		logger.WarnContext(ctx, "insecure Endpoints allowed: for local development only")
	}
	// A shrunk schedule gives up on a failing Endpoint within minutes rather
	// than about 24 hours.
	if cfg.shrunkSchedule {
		logger.WarnContext(ctx, "retry schedule shrunk: for local development only")
	}
	tp, flush, err := telemetry.Setup(ctx, "herald-dispatcher", cfg.shared.Telemetry, logger)
	if err != nil {
		return err
	}
	// Last, once nothing makes spans any more.
	defer flush(ctx)
	var started []any // The "started" log line's attributes.
	if cfg.shared.PprofAddr != "" {
		pprofAddr, stopPprof, err := profiling.Start(ctx, cfg.shared.PprofAddr, logger)
		if err != nil {
			return err
		}
		defer stopPprof()
		started = append(started, "pprof_addr", pprofAddr)
	}
	pool, err := pgxpool.New(ctx, cfg.shared.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	closed := make(chan struct{})
	nc, err := nats.Connect(cfg.shared.NATSURL,
		nats.Name("herald-dispatcher"),
		// Keep reconnecting however long NATS is away. Giving up would leave
		// the service running with no subscription, taking no Batches.
		nats.MaxReconnects(-1),
		nats.DrainTimeout(shutdownTimeout),
		nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
	)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	store := delivery.NewStore(pool)
	workers := delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:      cfg.shared.Keyring,
		Schedule:     cfg.schedule,
		Now:          time.Now,
		DrainTimeout: attemptDrainTimeout,

		AllowPrivateAddresses: cfg.shared.AllowInsecureEndpoints,
		TracerProvider:        tp,
	}, logger)
	intake := delivery.NewIntake(store, time.Now, tp, logger)
	if _, err := intake.Subscribe(ctx, nc, handoff.Subject, handoff.QueueGroup); err != nil {
		nc.Close()
		return err
	}
	workersDone := make(chan struct{})
	go func() {
		defer close(workersDone)
		// Returns once ctx is cancelled and the Attempts in flight are
		// recorded.
		workers.Run(ctx)
	}()
	logger.InfoContext(ctx, "started", started...)

	<-ctx.Done()
	// Draining stops new Batches arriving, lets the ones received finish and
	// closes the connection. The relay sends any it didn't get a reply for
	// again. The workers drain at the same time.
	if err := nc.Drain(); err != nil {
		logger.ErrorContext(ctx, "drain NATS connection", "error", err)
		nc.Close()
	}
	<-closed
	<-workersDone
	logger.InfoContext(ctx, "stopped")
	return nil
}
