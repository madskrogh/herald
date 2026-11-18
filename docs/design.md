# Herald design

Herald delivers webhooks on behalf of SaaS products (Tenants) to the HTTP Endpoints their customers (Applications) register: signed, retried, and observable. It is a showcase project built to production standards. It is meant to be run and read, not hosted.

Terms used here are defined in [GLOSSARY.md](../GLOSSARY.md). Decisions with significant trade-offs are recorded in [docs/adr/](adr/).

## Goals

- Reliable at-least-once delivery with duplicate protection on every hop.
- One misbehaving Endpoint must not affect deliveries to anyone else.
- Runs locally with `docker compose up`, and optionally on a local Kubernetes cluster (kind).
- Cloud-agnostic: no dependency on a specific cloud provider.

## Non-goals

- Tenant self-service (sign-up, billing, dashboards). Tenants are created with an admin CLI.
- A portal for Applications. Tenants show signing secrets and delivery history in their own products.
- Ordered delivery, content-based routing, payload transforms.
- Releases, hosting, or a marketing site.

## Architecture

```
Tenant ──REST/gRPC──▶ herald-api ──NATS request-reply──▶ herald-dispatcher ──HTTPS──▶ Endpoint
                          │      (queue group, batches)          │
                          ▼                                      ▼
                  Postgres: ingest schema              Postgres: delivery schema
```

- **herald-api** authenticates Tenants, manages Applications and Endpoints, accepts Messages, and works out each Message's Deliveries (event-type filter on each Endpoint). It writes the Message with `handed_off_at = NULL` in the same transaction. A relay claims rows that haven't been handed off (`FOR UPDATE SKIP LOCKED`), sends them in batches, and sets `handed_off_at` once a dispatcher replies. See [ADR 0002](adr/0002-two-services-outbox-nats-handoff.md). Each process runs one relay, which sends one batch at a time, and a dispatcher stores one batch at a time. So handoff stores at most as many batches at once as there are herald-api replicas, and dispatchers beyond that number add no handoff throughput (see Deferred).
- **herald-dispatcher** stores the Deliveries it receives (`ON CONFLICT DO NOTHING`) and replies only after the commit. Workers claim Deliveries that are due (`next_attempt_at <= now()`, `SKIP LOCKED`) and make the Attempts. From M3 it also serves the internal gRPC `DeliveryService`, which herald-api calls on behalf of Tenants.
- **herald-admin** is an operator CLI that connects directly to the `ingest` schema (`tenant create`, `apikey create`).
- Each service owns its schema's migrations. They are embedded with goose and run as a one-shot `migrate` step before the services start, never at startup.

## Delivery semantics

- **At-least-once** ([ADR 0001](adr/0001-at-least-once-delivery.md)). Every webhook carries a stable `webhook-id`, and receivers are expected to drop duplicates by it.
- **Idempotent submission.** `POST /v1/messages` accepts an optional `Idempotency-Key`, unique per Tenant for 24 hours (unique index on `(tenant_id, idempotency_key)`). A repeat (same Application, Event type and payload, compared byte for byte) returns the original Message and writes nothing. The same key with a different request is refused with `422` (`FAILED_PRECONDITION` over gRPC): returning the original would drop the new Message without telling the Tenant. Comparing bytes rather than parsed JSON errs loudly: a client that re-serializes a payload differently gets a `422`, never a silently dropped Message. Concurrent requests with the same key wait on the unique index (`ON CONFLICT DO NOTHING`), so one of them creates the Message. Keys expire lazily: a Message keeps its key after 24 hours, and the next request with that key takes it over, in the same transaction that creates the new Message. No background sweep is needed, and expiry is decided by herald-api's clock (`created_at` comes from it too).
- **Success:** any `2xx` response within **15 s**. Anything else (other status codes, timeout, connection error) is retried.
- **Retry schedule:** 8 Attempts, exponential backoff with jitter: immediately, 5 s, 1 m, 10 m, 1 h, 3 h, 6 h, 12 h (about 24 h in total).
- **Delivery states:** `pending` → `succeeded` | `failed`. A failed Delivery keeps its full Attempt history (status code, latency, start of the response body).
- **Replay** (M3): any Delivery, succeeded or failed, can be replayed through the API. A replay starts a fresh retry schedule with the same `webhook-id`.
- **Retention:** Deliveries and Attempts are deleted after 30 days.

## Isolation

- At most **4 Attempts in flight per Endpoint**, and the 15 s timeout limits how long any one Attempt holds a worker.
- Fairness across Tenants and per-Tenant rate limits are deferred.

## Security

- **Tenant authentication:** API keys stored as hashes, sent as a bearer token (REST) or in metadata (gRPC), each scoped to one Tenant.
- **Signing:** Standard Webhooks with HMAC-SHA256 ([ADR 0003](adr/0003-standard-webhooks-signing.md)). Each Endpoint has its own signing secrets (`whsec_` prefix), returned by the API when the Endpoint is created.
- **Endpoint addresses:** herald-dispatcher refuses to connect to any address that isn't globally reachable (loopback, link-local, private, unspecified, multicast, shared, reserved and documentation ranges, and IPv6 addresses that embed one), checking the address it actually dials, after DNS resolution. Otherwise an Endpoint such as `https://169.254.169.254/` would make it a proxy into Herald's network. A refused connection is a failed Attempt that says why, retried like any other. The Tenant API doesn't check this when an Endpoint is created, because a public host name can resolve to a private address later (DNS rebinding). herald-dispatcher connects to Endpoints directly, never through a proxy, which would hide the address it reaches.
- **Endpoint scheme:** Endpoint URLs must use `https`, checked when an Endpoint is created (`400`, `INVALID_ARGUMENT`, like any other invalid URL). Signing proves who sent a payload, but over plain `http` anyone on the path can read it, or answer `200` in the Endpoint's place. herald-dispatcher doesn't follow redirects, so an `https` Endpoint can't be downgraded.
- **Local development:** `HERALD_ALLOW_INSECURE_ENDPOINTS=true` relaxes both rules above, in herald-api and herald-dispatcher, for the local stack and tests, whose Endpoints are on private addresses without TLS. It is off unless set, and production never sets it.
- **Secrets at rest:** AES-GCM, with a key ID stored in each ciphertext and a keyring in config. Deliveries carry the encrypted copy, which the dispatcher decrypts just before signing.
- **Signing-secret rotation** (M4): the new secret is added and the old one expires after 48 h (longer than the retry window). Deliveries are signed with every secret that hasn't expired, and replays use the Endpoint's current secrets.

## Limits and validation

- Payload: a JSON value, at most **256 KiB** (`413` otherwise; `RESOURCE_EXHAUSTED` over gRPC). The API carries it as JSON text in a string field, and Herald stores and delivers those bytes unchanged: parsing it into `google.protobuf.Value` would turn numbers into `float64`, so integers above 2^53 would silently change.
- Idempotency-Key: 1 to 256 bytes of printable ASCII, as HTTP defines the header. The length keeps it within what the unique index can hold.
- Event type: `^[a-z0-9_]+(\.[a-z0-9_]+)*$`, at most **128 bytes**. Every Message in a handoff batch carries its Event type, so the cap bounds what each Message adds to a batch.
- Endpoints per Application: at most **100** (`422` otherwise; `FAILED_PRECONDITION` over gRPC). A Message's Deliveries travel in one handoff batch, which must stay under the NATS maximum message size, and each Delivery carries its Endpoint's URL (up to 2048 bytes) and signing secrets. With a 256 KiB payload, about 350 Deliveries would fill a 1 MB batch, so a Message to more Endpoints could never be handed off; 100 leaves a margin. Concurrent creates can't exceed it: each locks the Application row (`FOR NO KEY UPDATE`, which doesn't block Message inserts) before counting.
- Endpoint filter: NATS-style wildcards (`*` matches one token, `>` matches the rest). An empty filter matches everything. At most **32 patterns**, each at most **128 bytes**, which bounds the cost of matching one Endpoint during fan-out on the request path. A pattern may be as long as an Event type, so a filter can name any Event type.
- Tenant API REST request body: at most **1600 KiB** (`413` otherwise), checked before authentication. A payload travels as a JSON string, in which each byte may be escaped as six (`\u00XX`), so this is six times the largest payload plus 64 KiB for the other fields.
- Application name: not blank, at most **256 bytes**.
- Endpoint URL: an absolute `http` or `https` URL with a host and, if any, a port from 1 to 65535, at most **2048 bytes**, without user information (it would be stored in plain), a fragment (never sent) or unescaped whitespace.
- Handoff batches stay under the NATS maximum message size (1 MB by default).
- In v1, an Endpoint's URL and event filter can't be changed after creation (signing secrets can be rotated).

## API

- Protobuf first ([ADR 0004](adr/0004-protobuf-first-api.md)): `.proto` files with HTTP annotations, from which `buf` generates the gRPC server, the grpc-gateway REST/JSON gateway and an OpenAPI spec.
- The Tenant API is the protobuf package `herald.tenant.v1`, in a buf module of its own (`api/`). `buf breaking` checks it with the FILE rule set, because Tenants depend on more than its encoding: JSON field names through the gateway, gRPC method paths, and the names in clients generated from the protos. No buf rule checks the HTTP annotations; the integration tests pin each operation's REST method and path instead.
- Everything a Tenant types uses snake_case: REST JSON fields and query parameters use the proto field names (`application_id`, `page_size`), and multi-word path segments follow suit (`signing_secrets`). Each field has one name across gRPC, REST and the docs.
- The handoff between herald-api and herald-dispatcher is the internal protobuf package `herald.handoff.v1` (module `proto/`). It has no HTTP annotations and is never served through the gateway. `buf breaking` keeps it wire-compatible (the WIRE rule set), so both services' versions can run side by side during a rollout.
- Every Tenant API call carries an API key as `authorization: Bearer <key>`. Another Tenant's resource is reported as not found, never as forbidden, so IDs don't reveal what exists. List calls are paginated from the start (`page_size` up to 100, opaque `page_token`), since adding pagination later would silently truncate clients that don't page.
- Data access with sqlc on pgx ([ADR 0005](adr/0005-sqlc-instead-of-orm.md)).

## Stack

| Concern | Choice |
|---|---|
| HTTP | `net/http` (Go 1.22+ routing patterns) |
| Logging | `log/slog`, JSON, with trace IDs |
| Postgres | `pgx/v5` + sqlc, goose migrations embedded in the binaries |
| Messaging | `nats.go` (core request-reply, queue groups) |
| gRPC / REST | `google.golang.org/grpc`, grpc-gateway, `buf` |
| Observability | OpenTelemetry traces (context passed in NATS headers), stored in ClickHouse through the OpenTelemetry Collector ([ADR 0006](adr/0006-clickhouse-telemetry-store.md)); `net/http/pprof` |
| Tests | standard `testing`, `go-cmp`, testcontainers-go |

## Testing

- **Unit tests** for pure logic: routing and filters, the retry schedule, signing.
- **Integration tests** with testcontainers-go against real Postgres and NATS. No mocks of either.
- **End-to-end tests** run the full compose stack against a fake receiver that can be scripted to fail or slow down. They prove retries, duplicate protection and isolation.
- **Behaviour comparison.** End-to-end tests check what Herald should do. The comparison checks what it does, and shows when that changes. A scenario of realistic use runs against the `main` build and the candidate build. Herald has no production traffic, so the scenario stands in for it, and it covers only the inputs it generates: it catches unintended changes in behaviour, not inputs nobody imagined. To cover as much as it can:
  - **Generated traffic from a seed.** Applications, Endpoints with varied filters, and Messages with varied Event types, payload sizes and Idempotency-Keys are generated from a seed that each run records, so a run can be repeated exactly.
  - **Endpoints that behave like real ones:** success, failures then success, slow responses, timeouts, connection resets, oversized or malformed responses, redirects, TLS errors, and Endpoints that flap.
  - **Faults during the run:** a dispatcher restarted, NATS paused, Postgres slowed down.
  - **Every bug becomes a case.** A fix for a bug that reached `main` adds the case that shows it to the scenario, so the scenario grows from what actually went wrong.

  Two witnesses are compared:
  - **The edge recording:** what the fake Endpoints received (Endpoint, headers, body, order, time since the Delivery's first Attempt) and each Delivery's final state. What varies between runs (IDs, timestamps, retry jitter) is normalised, and signatures are verified rather than compared byte for byte. It is taken outside Herald, so a change to Herald's instrumentation can't change it.
  - **Telemetry statistics:** per run, from the spans: Attempts by outcome, retry timing, Attempt latency and handoff lag, each with a margin.

  The expected results are committed. A difference fails the comparison unless the PR updates them, so an intended change shows up as a diff the reviewer approves, and a change that should alter nothing must leave them alone. Recordings keep whatever the recorded version did, bugs included: they show change, not correctness. "Replay" is a Delivery operation, so this feature avoids the word.
- **Telemetry invariants,** checked after every scenario run: every accepted Message has a trace that reaches its Attempts, no span lacks its parent, no span records an unexpected error, handoff lag and how late each Attempt starts stay under fixed limits, and each service's goroutines return to their idle set (from the goroutine profile, compared by stack, which names the leak when it fails). Every check fails on missing data: a query that returns no rows, or rows without an expected attribute, is a failure, never a pass.
- **Load tests** (`make loadtest`) run the scenario scaled up, with several replicas of each service, on demand. They don't run on PRs, where shared runners make timings too noisy to gate on. They settle the tuning questions under Deferred that wait on load.

## Observability

- Both services emit OpenTelemetry traces. A trace starts at the Tenant API call and follows the Message through the Outbox, the handoff (context carried in NATS headers) and every Attempt.
- Each handoff and each Attempt is one wide span carrying everything known at that point: Tenant, Application, Endpoint, Message, Delivery, attempt number, outcome, status code, latency, Batch size, error and build version. A question nobody planned for is a new query, not new instrumentation.
- The span schema (span names and attribute keys) is defined in one place, and a test fails when a service emits a span that doesn't match it. Renaming or dropping an attribute is a visible change, not a check that silently reads nothing.
- The OpenTelemetry Collector writes spans to ClickHouse, where checks and people query them with SQL ([ADR 0006](adr/0006-clickhouse-telemetry-store.md)). There are no separately instrumented metrics: rates and distributions are queries over spans, and current state (Deliveries by state) is a query on Postgres.
- Each scenario run sets a run ID and the build version as resource attributes, so the runs being compared share one store.
- Each service serves `net/http/pprof` on an internal port, separate from the Tenant-facing ones. `make profile` captures CPU, heap and allocation profiles and an execution trace from both builds while the scenario runs, and diffs them (`go tool pprof -diff_base`). Profiles aren't a gate, because CPU time on a small, I/O-bound scenario is noise. They explain a difference the comparison found, or back a claim about performance.
- Logs stay `log/slog` JSON with trace IDs.

## CI (GitHub Actions)

On every PR, and on `main` after each merge: `golangci-lint`, `go test -race` (unit and integration), `buf lint` and `buf breaking` against `main`, a check that sqlc and protobuf generated code is up to date, Docker image builds, and against the compose stack the end-to-end tests, the behaviour comparison and the telemetry invariants.

## Milestones

1. **M1: Core delivery path.** Admin CLI (Tenant, API key). Tenant API for Applications, Endpoints and Messages, with idempotency keys. Outbox → NATS → dispatcher. Signed Attempts with retries and the per-Endpoint concurrency cap. Deliveries end up `succeeded` or `failed`. Structured logs. `docker compose up`, plus an end-to-end test.
2. **M2: Observability and behaviour comparison.** OpenTelemetry traces across api → NATS → dispatcher, stored in ClickHouse through the Collector, with a span schema the tests enforce. The scenario, its edge recording and telemetry statistics, compared between `main` and the candidate, and the telemetry invariants, on every PR. pprof on an internal port, `make profile`, and `make loadtest`, used to settle the tuning questions under Deferred that wait on load. Every later milestone is checked against this.
3. **M3: Delivery API and replay.** Internal gRPC `DeliveryService` through herald-api: status, Attempt history, replay. Retention job.
4. **M4: Rotation.** Signing-secret rotation, and encryption-key rotation through the keyring.
5. **M5: Kubernetes.** kind and Kustomize manifests, migrations as a Job, `make kind-up`.

## Deferred

- Changing an Endpoint's URL or filter (needs an "endpoint changed" message from api to dispatcher).
- Fairness across Tenants and per-Tenant rate limits.
- Re-encrypting stored secrets after an encryption-key rotation.
- Tenant self-service and an admin API.
- Ordered delivery.
- Scaling handoff past one relay per herald-api replica. When the load test shows handoff lag growing because the relays can't keep up, run several relays per process, each its own `outbox.Relay` with its own claim limit (`SKIP LOCKED` keeps their claims apart), behind a setting. Each relay holds a pooled database connection for up to the request timeout while it waits for a reply, so the pool must grow with it, and there must be at least as many dispatchers as relays across all replicas.
- A setting for the number of delivery workers per dispatcher, fixed today at 32, a guess that no measurement backs. Make it configurable once the load test shows what to set it to.
- Holding Endpoint claim locks for a whole claim. A dispatcher claims for all its due Endpoints in one transaction and holds each Endpoint's lock until that transaction ends. Another replica that finds an Endpoint locked skips it until its next round, up to 500 ms later. With few busy Endpoints and several replicas, those Endpoints' Deliveries could keep slipping by a round. To evaluate: a load test with several dispatcher replicas and a few busy Endpoints, measuring how late each Attempt starts after its Delivery falls due (from the Attempt spans). If the lateness shows, claim each Endpoint in its own transaction, which frees its lock right after its leases at the cost of a commit per Endpoint.
- Grafana on ClickHouse, for browsing spans. Checks and ad hoc questions need only SQL.
- Benchmarks for hot pure code (signing, filter matching, Batch encoding), once a profile or the load test shows a hot spot. An Attempt waits up to 15 s on the network; nothing yet shows these paths matter.
- Continuous profiling, which pays off only with sustained traffic. Herald is run and read, not hosted.
