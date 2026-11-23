# The OpenTelemetry Collector with Herald's configuration. The version is
# pinned: the span checks are written against its ClickHouse exporter's
# table layout (docs/adr/0006-clickhouse-telemetry-store.md).
#
# The Collector's image has no shell or HTTP client, so a static busybox wget
# is added for compose's health check.
FROM busybox:1.37.0-musl AS busybox

FROM otel/opentelemetry-collector-contrib:0.165.0
COPY --from=busybox /bin/wget /bin/wget
COPY config.yaml /etc/otelcol-contrib/config.yaml
