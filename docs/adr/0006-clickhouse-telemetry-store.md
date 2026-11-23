# ClickHouse as the telemetry store

Herald's traces go through the OpenTelemetry Collector into ClickHouse, using the Collector's ClickHouse exporter. Each span is one row, with its attributes as maps. The scenario invariants, the behaviour comparison and ad hoc questions are all SQL over those rows.

The telemetry exists to be queried by checks that run on every PR, and by whoever investigates what a change did, without knowing the question in advance. That calls for wide spans (every attribute known at a handoff or an Attempt), grouping by any of them (run, build version, Endpoint, Tenant), and checks across a trace's spans ("every handoff reached an Attempt"). SQL over a columnar store does all three in one query, and its results are text that fits in a test failure or a PR description.

## Considered Options

- **Jaeger and Prometheus**: rejected. Comparing runs needs metrics designed in advance, with labels for run and version, and a label per Endpoint or Tenant inflates Prometheus's cardinality, so those questions can't be asked. Jaeger looks traces up by service and tag with a limit, so a check across traces fetches them all and walks the span trees in Go. Its strengths, browsing one trace and familiar dashboards, serve a person at a UI, not a check.
- **Grafana's LGTM stack (Tempo, Prometheus, Loki, Grafana)**: rejected. TraceQL selects spans by attribute, but grouping and comparing two runs by version is clumsier than SQL, and it adds more to the stack than a store for spans.
- **Honeycomb**: rejected. It is built for exactly these queries, but it is a hosted service: it needs an account and a CI secret, and doesn't run from a clean checkout. Herald must run locally and stay cloud-agnostic. Because services emit plain OpenTelemetry, a Collector export to it can be added later without touching Herald's code.

## Consequences

- The stack gains two containers, the Collector and ClickHouse, locally and in CI.
- The tables belong to the exporter. Checks are written against its layout (`otel_traces`), so the Collector's version is pinned and an upgrade that changes the layout shows up as failing checks.
- Checks query ClickHouse over its HTTP interface with `net/http`, and need no client library. Their queries are `.sql` files with ClickHouse query parameters, never SQL built in Go. sqlc doesn't support ClickHouse, so ADR 0005's rule applies to the database Herald stores its data in, Postgres, and these files are the exception.
- Spans are reported by the code under test, so a change can alter what the checks read. The span schema test and checks that fail on missing data guard against a check passing on nothing. The edge recording, taken outside Herald, is the witness that instrumentation can't change.
- There is no UI for browsing spans until Grafana is added (Deferred). Checks and investigations need only SQL.
