// Package profiling serves net/http/pprof on a service's internal port,
// separate from the Tenant-facing ones.
package profiling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

// Start listens on addr and serves the pprof endpoints under /debug/pprof/
// there in the background. It returns the address it listens on, which
// differs from addr when addr has port 0, and a function that stops serving.
// The endpoints are on a mux of their own: importing net/http/pprof also
// registers them on http.DefaultServeMux, which no server in Herald serves.
//
// Stopping doesn't wait for a profile in progress. A failure to serve once
// listening is logged and leaves the service running, since profiling isn't
// its job.
func Start(ctx context.Context, addr string, logger *slog.Logger) (listening string, stop func(), err error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("listen for pprof: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: a CPU profile or an execution trace streams for
		// as long as the caller asks.
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(lis); !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "serve pprof", "error", err)
		}
	}()
	return lis.Addr().String(), func() {
		srv.Close()
		<-done
	}, nil
}
