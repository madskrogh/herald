// Command herald-api is Herald's Tenant-facing service. It authenticates
// Tenants, manages Applications and Endpoints, accepts Messages and hands
// their Deliveries off to herald-dispatcher.
//
// Usage:
//
//	herald-api            run the service
//	herald-api migrate    apply the ingest schema's migrations and exit
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

	"github.com/madskrogh/herald/internal/ingest"
)

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
		return serve(ctx, logger)
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
		return ingest.Migrate(ctx, dbURL, logger)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// serve runs the service until ctx is cancelled.
func serve(ctx context.Context, logger *slog.Logger) error {
	logger.InfoContext(ctx, "started")
	<-ctx.Done()
	logger.InfoContext(ctx, "stopped")
	return nil
}
