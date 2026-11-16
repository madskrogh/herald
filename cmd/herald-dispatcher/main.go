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
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/madskrogh/herald/internal/delivery"
	"github.com/madskrogh/herald/internal/handoff"
	"github.com/madskrogh/herald/internal/keyring"
	"github.com/madskrogh/herald/internal/retry"
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
	logger := slog.New(slog.NewJSONHandler(stdout, nil)).With("service", "herald-dispatcher")

	if len(args) == 0 {
		cfg, err := loadConfig(getenv)
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
		dbURL := getenv("HERALD_DATABASE_URL")
		if dbURL == "" {
			return errors.New("HERALD_DATABASE_URL is not set")
		}
		return delivery.Migrate(ctx, dbURL, logger)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// config is the service's configuration.
type config struct {
	databaseURL string
	keyring     *keyring.Keyring
	natsURL     string
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		databaseURL: getenv("HERALD_DATABASE_URL"),
		natsURL:     getenv("HERALD_NATS_URL"),
	}
	if cfg.databaseURL == "" {
		return config{}, errors.New("HERALD_DATABASE_URL is not set")
	}
	keys := getenv("HERALD_KEYRING")
	if keys == "" {
		return config{}, errors.New("HERALD_KEYRING is not set")
	}
	kr, err := keyring.Parse(keys)
	if err != nil {
		return config{}, fmt.Errorf("HERALD_KEYRING: %w", err)
	}
	cfg.keyring = kr
	if cfg.natsURL == "" {
		return config{}, errors.New("HERALD_NATS_URL is not set")
	}
	return cfg, nil
}

// serve stores the Batches handed off by herald-api and makes their
// Attempts until ctx is cancelled. Then it lets the Batches already received
// and the Attempts in flight finish.
func serve(ctx context.Context, cfg config, logger *slog.Logger) error {
	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	closed := make(chan struct{})
	nc, err := nats.Connect(cfg.natsURL,
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
	// Scale 1: the retry schedule at full length, as docs/design.md gives it.
	schedule, err := retry.New(1, rand.Float64)
	if err != nil {
		nc.Close()
		return fmt.Errorf("retry schedule: %w", err)
	}
	workers := delivery.NewWorkers(store, delivery.WorkersConfig{
		Keyring:      cfg.keyring,
		Schedule:     schedule,
		Now:          time.Now,
		DrainTimeout: attemptDrainTimeout,
	}, logger)
	intake := delivery.NewIntake(store, time.Now, logger)
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
	logger.InfoContext(ctx, "started")

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
