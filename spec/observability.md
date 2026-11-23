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

Every span Herald emits MUST be one the [span schema](span-schema.md) names, with exactly the attributes the schema gives it. An attribute that doesn't apply MUST be set to its zero value, never left out.

Why: each span carries everything known at that point, so a question nobody planned for is a new query, not new instrumentation. A fixed set of keys makes renaming or dropping one a visible change, not a check that silently reads nothing, and a check can tell a missing attribute from an empty one.

| Span | Attributes | Follows the schema | Names |
|---|---|---|---|
| `handoff.batch` | `herald.batch_size=2`, `herald.outcome=handed_off`, `herald.error=` | yes | |
| `handoff.batch` | `herald.error=`, `herald.outcome=handed_off`, `herald.batch_size=2` | yes | |
| `handoff.batch` | `herald.batch_size=2`, `herald.outcome=handed_off` | no | `herald.error` |
| `handoff.batch` | `herald.batch_size=2`, `herald.outcome=handed_off`, `herald.error=`, `herald.tenant_id=0198a1b2-0000-7000-8000-0000000000aa` | no | `herald.tenant_id` |
| `batch` | `herald.batch_size=2`, `herald.outcome=handed_off`, `herald.error=` | no | `batch` |

### R-71 · implemented

Each attribute of a span MUST have the type the [span schema](span-schema.md) gives its key, and MUST NOT be empty unless the schema allows it. An `int` attribute MUST be emitted as an integer, and any other as a string.

Why: a query compares and sums attribute values, so an ID that isn't a UUID or a count that isn't a number reads as nothing, silently. Empty is allowed only where the value can be unknown, so a missing ID is a visible change. Emitting a number as an integer keeps its type in OTLP for any store that keeps it.

| Span | Attributes | Follows the schema | Names |
|---|---|---|---|
| `handoff.batch` | `herald.batch_size=two`, `herald.outcome=handed_off`, `herald.error=` | no | `herald.batch_size` |
| `handoff.batch` | `herald.batch_size=`, `herald.outcome=handed_off`, `herald.error=` | no | `herald.batch_size` |
| `handoff.batch` | `herald.batch_size=3`, `herald.outcome=failed`, `herald.error=nats: timeout` | yes | |
| `tenant_api.call` | `rpc.method=/herald.tenant.v1.MessageService/CreateMessage`, `herald.tenant_id=`, `herald.outcome=Unauthenticated`, `herald.error=no API key` | yes | |
| `tenant_api.call` | `rpc.method=/herald.tenant.v1.MessageService/CreateMessage`, `herald.tenant_id=0198A1B2-0000-7000-8000-0000000000AA`, `herald.outcome=OK`, `herald.error=` | no | `herald.tenant_id` |
| `tenant_api.call` | `rpc.method=/herald.tenant.v1.MessageService/CreateMessage`, `herald.tenant_id=tenant-1`, `herald.outcome=OK`, `herald.error=` | no | `herald.tenant_id` |
| `tenant_api.call` | `rpc.method=`, `herald.tenant_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.outcome=OK`, `herald.error=` | no | `rpc.method` |
| `handoff` | `herald.tenant_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.application_id=`, `herald.message_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.batch_size=1`, `herald.delivery_count=0`, `herald.outcome=handed_off`, `herald.error=` | yes | |
| `handoff` | `herald.tenant_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.application_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.message_id=`, `herald.batch_size=1`, `herald.delivery_count=0`, `herald.outcome=handed_off`, `herald.error=` | no | `herald.message_id` |

### R-72 · implemented

The `herald.outcome` attribute of a span MUST be one of the values the [span schema](span-schema.md) lists for that span.

Why: Attempts, handoffs and calls are counted by outcome, so a value outside the list is a count nobody queries.

| Span | Attributes | Follows the schema | Names |
|---|---|---|---|
| `handoff.batch` | `herald.batch_size=2`, `herald.outcome=failed`, `herald.error=nats: timeout` | yes | |
| `handoff.batch` | `herald.batch_size=2`, `herald.outcome=parked`, `herald.error=` | no | `herald.outcome` |
| `tenant_api.call` | `rpc.method=/herald.tenant.v1.MessageService/GetMessage`, `herald.tenant_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.outcome=NotFound`, `herald.error=message not found` | yes | |
| `tenant_api.call` | `rpc.method=/herald.tenant.v1.MessageService/GetMessage`, `herald.tenant_id=0198a1b2-0000-7000-8000-0000000000aa`, `herald.outcome=NOT_FOUND`, `herald.error=message not found` | no | `herald.outcome` |

### R-73 · implemented

A span's parent, and each span it links to, MUST be one the [span schema](span-schema.md) allows for it. A span MUST have a parent unless the schema allows it to be a root, and MUST come from the service the schema names.

Why: the trace shape is what a developer follows from one Message to every step of its journey ([R-51](#r-51--implemented), [R-52](#r-52--implemented)); a span under the wrong parent, or from the wrong service, breaks the path without breaking the trace.

| Span | Service | Parent | Links to | Follows the schema | Names |
|---|---|---|---|---|---|
| `handoff` | herald-api | `tenant_api.call` | | yes | |
| `handoff` | herald-api | none | | yes | |
| `handoff` | herald-dispatcher | `tenant_api.call` | | no | herald-dispatcher |
| `store_deliveries` | herald-dispatcher | `tenant_api.call` | | no | `tenant_api.call` |
| `attempt` | herald-dispatcher | `store_deliveries` | | yes | |
| `attempt` | herald-dispatcher | `attempt` | | no | `attempt` |
| `tenant_api.call` | herald-api | `tenant_api.call` | | no | `tenant_api.call` |
| `handoff.batch` | herald-api | none | `handoff`, `handoff` | yes | |
| `handoff.batch` | herald-api | none | `attempt` | no | `attempt` |
| `handoff.batch` | herald-api | `tenant_api.call` | `handoff` | no | `tenant_api.call` |
| `handoff` | herald-api | `tenant_api.call` | `handoff` | no | `handoff` |

### R-74 · implemented

An `attempt` span's `herald.error` MUST NOT hold the Endpoint's URL, or any part of its path or query, even when the Attempt's own error, which the Tenant sees, does.

Why: spans leave Herald for the Collector and the telemetry store, which the Tenant's secrets don't otherwise reach, and an Endpoint URL may carry a token in its query (decided in #77).

| Endpoint URL | Attempt | Attempt's error names | `herald.error` |
|---|---|---|---|
| `http://127.0.0.1:1/webhooks?token=tok_9f2c41d7` | connection refused | the URL, with `tok_9f2c41d7` | why the connection failed, without `tok_9f2c41d7` |

### R-75 · planned

A Tenant API call's span MUST start a new trace, whatever trace context the Tenant sends with the call.

Why: a Tenant then can't choose Herald's trace IDs, join its own traces to Herald's, or turn Herald's sampling off.

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
| an Attempt | `time`, `level`, `msg`, `service`, `delivery_id`, `message_id`, `endpoint_id`, `attempt`, `status_code`, `latency_ms`, `state`, `trace_id`, `span_id`, and `attempt_error` when the Attempt got no response |
| a line with no current span | no `trace_id` or `span_id` |

## Profiles

### R-57 · implemented

When `HERALD_PPROF_ADDR` is set, each service MUST serve Go's `net/http/pprof` profiles under `/debug/pprof/` on that address, and on no other. When it isn't set, the service MUST NOT serve them.

Why: a developer needs profiles from a running service, but they reveal its internals, so they are served only on an internal address, never on a Tenant-facing one.

| `HERALD_PPROF_ADDR` | `/debug/pprof/` on that address | `/debug/pprof/` on the Tenant API's port |
|---|---|---|
| `:6060` | served | not found |
| not set | n/a | not found |
