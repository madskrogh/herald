package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config says where a service's spans go and what identifies them.
type Config struct {
	// OTLPURL is the Collector's OTLP/HTTP base URL, such as
	// http://otelcol:4318, or empty to export nothing.
	OTLPURL string
	// RunID identifies the run that the spans belong to, such as one run of
	// the behaviour comparison's scenario, or is empty.
	RunID string
	// BuildVersion identifies the build that emits the spans.
	BuildVersion string
}

// The resource attributes every span of a service carries: they identify
// the service, the build and the run, so that the runs being compared can
// share one store.
const (
	ServiceName    attribute.Key = "service.name"
	ServiceVersion attribute.Key = "service.version"
	RunID          attribute.Key = "herald.run_id"
)

// flushTimeout is how long the spans still waiting get to be exported at
// shutdown. With the services' own shutdown timeouts, of at most 8 seconds,
// it stays under the 10 seconds Docker and Kubernetes wait before killing a
// container.
const flushTimeout = time.Second

// Setup returns the TracerProvider of the service named service
// (newTracerProvider), and flush, which exports the spans still waiting and
// is called once, at shutdown. Spans that can't be exported are dropped, and
// logged to logger: the service goes on without them. Setup sets the
// process's OpenTelemetry error handler, so a service calls it once, from
// main.
//
// Without an OTLPURL it warns that spans aren't exported, so that a
// deployment that forgets it says so as it starts.
func Setup(ctx context.Context, service string, cfg Config, logger *slog.Logger) (tp *sdktrace.TracerProvider, flush func(context.Context), err error) {
	tp, err = newTracerProvider(ctx, service, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("set up tracing: %w", err)
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.WarnContext(ctx, "export spans", "error", err)
	}))
	if cfg.OTLPURL == "" {
		logger.WarnContext(ctx, "HERALD_OTLP_URL is not set: spans are not exported")
	}
	flush = func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			logger.WarnContext(ctx, "spans not exported at shutdown", "error", err)
		}
	}
	return tp, flush, nil
}

// newTracerProvider returns the TracerProvider of the service named service,
// which sends spans to the Collector at cfg.OTLPURL in batches. Shut it down
// to send the spans still waiting.
//
// Without an OTLPURL it exports nothing, but its spans still have contexts,
// which Messages and Deliveries store: a service started with an OTLPURL
// later continues their traces.
func newTracerProvider(ctx context.Context, service string, cfg Config) (*sdktrace.TracerProvider, error) {
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewSchemaless(
			ServiceName.String(service),
			ServiceVersion.String(cfg.BuildVersion),
			RunID.String(cfg.RunID),
		)),
	}
	if cfg.OTLPURL != "" {
		// The exporter falls back to localhost on a URL it can't use, so it
		// is checked here.
		u, err := url.Parse(cfg.OTLPURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("OTLP URL %q is not an http or https URL with a host", cfg.OTLPURL)
		}
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(u.JoinPath("v1/traces").String()))
		if err != nil {
			return nil, fmt.Errorf("start OTLP exporter for %s: %w", cfg.OTLPURL, err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}
	return sdktrace.NewTracerProvider(opts...), nil
}
