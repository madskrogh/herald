// Command herald-dispatcher stores the Deliveries handed off by herald-api
// and makes signed Attempts for them, retrying each Delivery on a schedule
// and capping the Attempts in flight per Endpoint.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	err := run(ctx, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "herald-dispatcher:", err)
		os.Exit(1)
	}
}

// run starts the service, logging JSON to stdout, and blocks until ctx is
// cancelled.
func run(ctx context.Context, stdout io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(stdout, nil)).With("service", "herald-dispatcher")

	logger.InfoContext(ctx, "started")
	<-ctx.Done()
	logger.InfoContext(ctx, "stopped")
	return nil
}
