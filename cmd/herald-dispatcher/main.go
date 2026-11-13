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
//	HERALD_NATS_URL       NATS server URL (required to run the service)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/madskrogh/herald/internal/delivery"
)

const (
	// handoffSubject is the NATS subject the relay in herald-api sends
	// handoff Batches to (ADR 0002).
	handoffSubject = "herald.handoff.v1"
	// queueGroup spreads the Batches across the running dispatchers: each
	// Batch goes to one of them.
	queueGroup = "herald-dispatcher"
	// shutdownTimeout is how long the Batches already received get to finish
	// once shutdown starts. It stays under the 10 seconds Docker and
	// Kubernetes wait before killing a container.
	shutdownTimeout = 8 * time.Second
)

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
	if cfg.natsURL == "" {
		return config{}, errors.New("HERALD_NATS_URL is not set")
	}
	return cfg, nil
}

// serve stores the Batches handed off by herald-api until ctx is cancelled,
// then lets the Batches already received finish.
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
	intake := delivery.NewIntake(delivery.NewStore(pool), time.Now, logger)
	if _, err := intake.Subscribe(ctx, nc, handoffSubject, queueGroup); err != nil {
		nc.Close()
		return err
	}
	logger.InfoContext(ctx, "started")

	<-ctx.Done()
	// Draining stops new Batches arriving, lets the ones received finish and
	// closes the connection. The relay sends any it didn't get a reply for
	// again.
	if err := nc.Drain(); err != nil {
		logger.ErrorContext(ctx, "drain NATS connection", "error", err)
		nc.Close()
	}
	<-closed
	logger.InfoContext(ctx, "stopped")
	return nil
}
