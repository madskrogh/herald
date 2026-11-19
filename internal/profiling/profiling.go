// Package profiling serves net/http/pprof on a service's internal port,
// separate from the Tenant-facing ones.
package profiling

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

// Start serves the pprof endpoints under /debug/pprof/ on lis in the
// background, and returns a function that stops serving. The endpoints are on
// a mux of their own: importing net/http/pprof also registers them on
// http.DefaultServeMux, which no server in Herald serves.
//
// Stopping doesn't wait for a profile in progress. A failure to serve is
// logged and leaves the service running, since profiling isn't its job.
func Start(ctx context.Context, lis net.Listener, logger *slog.Logger) (stop func()) {
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
	return func() {
		srv.Close()
		<-done
	}
}
