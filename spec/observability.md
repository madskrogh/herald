# Observability

The debugging data Herald leaves: traces, logs and profiles. The spans are stored in ClickHouse through the OpenTelemetry Collector, where checks and people query them with SQL ([ADR 0006](../docs/adr/0006-clickhouse-telemetry-store.md)). Rates and distributions are queries over the spans, so there are no separately instrumented metrics.

## Traces

### R-51 · implemented

Each Message accepted MUST have one trace, rooted at the Tenant API call that created it, that reaches its handoff, the storing of its Deliveries and every Attempt, however much later and on whichever replica each runs.

Why: one Message's whole journey is then one trace tree, so a developer follows it from one ID without joining logs from several services by hand.

| Span | Service | Parent |
|---|---|---|
| `tenant_api.call` | herald-api | none |
| `handoff` | herald-api | `tenant_api.call` |
| `store_deliveries` | herald-dispatcher | `handoff` |
| `attempt`, for Attempt 1 | herald-dispatcher | `store_deliveries` |
| `attempt`, for Attempt 7, six hours later on another dispatcher | herald-dispatcher | `store_deliveries` |

### R-52 · implemented

Each Batch MUST have a `handoff.batch` span that starts a trace of its own and links to the `handoff` span of each Message in the Batch.

Why: a Batch holds Messages from many traces, so it can't have one parent; the links let a developer get from a Batch to its Messages and back.

| Batch | `handoff.batch` parent | `handoff.batch` links |
|---|---|---|
| Messages M1 and M2, from two traces | none | M1's `handoff`, M2's `handoff` |

### R-53 · implemented

Every span Herald emits MUST be one the span schema names, with exactly the attributes the schema gives it. An attribute that doesn't apply MUST be set to its zero value, never left out.

Why: each span carries everything known at that point, so a question nobody planned for is a new query, not new instrumentation. A fixed set of keys makes renaming or dropping one a visible change, not a check that silently reads nothing, and a check can tell a missing attribute from an empty one.

| Span | Attributes | `herald.outcome` |
|---|---|---|
| `tenant_api.call` | `rpc.method`, `herald.tenant_id`, `herald.outcome`, `herald.error` | the call's gRPC status code, such as `OK` or `NotFound` |
| `handoff.batch` | `herald.batch_size`, `herald.outcome`, `herald.error` | `handed_off`, `failed` |
| `handoff` | `herald.tenant_id`, `herald.application_id`, `herald.message_id`, `herald.batch_size`, `herald.delivery_count`, `herald.outcome`, `herald.error` | `handed_off`, `parked`, `failed` |
| `store_deliveries` | `herald.tenant_id`, `herald.application_id`, `herald.message_id`, `herald.batch_size`, `herald.delivery_count`, `herald.outcome`, `herald.error` | `stored`, `failed` |
| `attempt` | `herald.tenant_id`, `herald.application_id`, `herald.endpoint_id`, `herald.message_id`, `herald.delivery_id`, `herald.attempt`, `herald.outcome`, `herald.status_code`, `herald.latency_ms`, `herald.lateness_ms`, `herald.error` | `succeeded`, `pending`, `failed`, `cut_off`, `not_recorded` |

### R-54 · implemented

When `HERALD_OTLP_URL` is set, each service MUST export its spans over OTLP/HTTP to it. When it isn't, each service MUST export nothing, and still store each Message's and Delivery's trace context.

Why: the Collector is the one place spans go ([ADR 0006](../docs/adr/0006-clickhouse-telemetry-store.md)). Storing the context either way means a service started with the setting later continues the traces of work already accepted.

| `HERALD_OTLP_URL` | Spans sent to |
|---|---|
| `http://otelcol:4318` | `http://otelcol:4318/v1/traces` |
| not set | nowhere |
| `otelcol:4318` | the service doesn't start |

### R-55 · implemented

Every span MUST carry the resource attributes `service.name` (`herald-api` or `herald-dispatcher`), `service.version` (`HERALD_BUILD_VERSION`, or `dev` if it isn't set) and `herald.run_id` (`HERALD_RUN_ID`, or empty).

Why: runs of different builds share one store, so a query compares them by version and run.

| Service | `HERALD_BUILD_VERSION` | `HERALD_RUN_ID` | `service.name` | `service.version` | `herald.run_id` |
|---|---|---|---|---|---|
| herald-api | `abc123` | `run-7` | `herald-api` | `abc123` | `run-7` |
| herald-dispatcher | not set | not set | `herald-dispatcher` | `dev` | (empty) |

## Logs

### R-56 · implemented

Each service MUST log JSON lines to standard output, and every line logged while a span is current MUST carry that span's `trace_id` and `span_id`.

Why: a log line then finds its span, and the span its log lines, so a developer moves between the two by ID.

| Logged | Fields |
|---|---|
| an Attempt | `time`, `level`, `msg`, `service`, `delivery_id`, `message_id`, `endpoint_id`, `attempt`, `status_code`, `latency_ms`, `state`, `trace_id`, `span_id`, and `attempt_error` when it failed |
| a line with no current span | no `trace_id` or `span_id` |

## Profiles

### R-57 · implemented

When `HERALD_PPROF_ADDR` is set, each service MUST serve Go's `net/http/pprof` profiles under `/debug/pprof/` on that address, and on no other. When it isn't set, the service MUST NOT serve them.

Why: profiles explain a difference in behaviour or back a claim about performance. They reveal a service's internals, so they are served only on an internal address, never on a Tenant-facing one.

| `HERALD_PPROF_ADDR` | `/debug/pprof/` on that address | `/debug/pprof/` on the Tenant API's port |
|---|---|---|
| `:6060` | served | not found |
| not set | n/a | not found |
