// Command demo-endpoint serves an Endpoint for trying Herald out against the
// local stack: it prints each Attempt it receives, and whether its signature
// verifies. Run it with `make endpoint`; the README walks through it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/madskrogh/herald/internal/demoendpoint"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo-endpoint:", err)
		os.Exit(1)
	}
}

// run serves the Endpoint until ctx is cancelled.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("demo-endpoint", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:9000", "address to listen on")
	endpointURL := fs.String("url", "", "the URL Herald reaches this Endpoint at, printed to register it with")
	secret := fs.String("secret", "", "the Endpoint's signing secret, to verify signatures with")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *listen, err)
	}
	if *endpointURL != "" {
		fmt.Fprintf(stdout, "Serving an Endpoint at %s\n", *endpointURL)
	}
	if *secret == "" {
		fmt.Fprintln(stdout, "No signing secret given, so signatures are not checked.")
	}
	fmt.Fprintln(stdout, "Waiting for Attempts. Stop with Ctrl-C.")
	fmt.Fprintln(stdout)

	srv := &http.Server{
		Handler:           demoendpoint.Handler(stdout, *secret, time.Now),
		ReadHeaderTimeout: 10 * time.Second,
	}
	context.AfterFunc(ctx, func() { srv.Close() })
	if err := srv.Serve(lis); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve on %s: %w", *listen, err)
	}
	return nil
}
