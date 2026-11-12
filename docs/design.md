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

- **herald-api** authenticates Tenants, manages Applications and Endpoints, accepts Messages, and works out each Message's Deliveries (event-type filter on each Endpoint). It writes the Message with `handed_off_at = NULL` in the same transaction. A relay claims rows that haven't been handed off (`FOR UPDATE SKIP LOCKED`), sends them in batches, and sets `handed_off_at` once a dispatcher replies. See [ADR 0002](adr/0002-two-services-outbox-nats-handoff.md).
- **herald-dispatcher** stores the Deliveries it receives (`ON CONFLICT DO NOTHING`) and replies only after the commit. Workers claim Deliveries that are due (`next_attempt_at <= now()`, `SKIP LOCKED`) and make the Attempts. From M2 it also serves the internal gRPC `DeliveryService`, which herald-api calls on behalf of Tenants.
- **herald-admin** is an operator CLI that connects directly to the `ingest` schema (`tenant create`, `apikey create`).
- Each service owns its schema's migrations. They are embedded with goose and run as a one-shot `migrate` step before the services start, never at startup.

## Delivery semantics

- **At-least-once** ([ADR 0001](adr/0001-at-least-once-delivery.md)). Every webhook carries a stable `webhook-id`, and receivers are expected to drop duplicates by it.
- **Idempotent submission.** `POST /v1/messages` accepts an optional `Idempotency-Key`, unique per Tenant for 24 hours (unique index on `(tenant_id, idempotency_key)`). A repeat returns the original Message.
- **Success:** any `2xx` response within **15 s**. Anything else (other status codes, timeout, connection error) is retried.
- **Retry schedule:** 8 Attempts, exponential backoff with jitter: immediately, 5 s, 1 m, 10 m, 1 h, 3 h, 6 h, 12 h (about 24 h in total).
- **Delivery states:** `pending` → `succeeded` | `failed`. A failed Delivery keeps its full Attempt history (status code, latency, start of the response body).
- **Replay** (M2): any Delivery, succeeded or failed, can be replayed through the API. A replay starts a fresh retry schedule with the same `webhook-id`.
- **Retention:** Deliveries and Attempts are deleted after 30 days.

## Isolation

- At most **4 Attempts in flight per Endpoint**, and the 15 s timeout limits how long any one Attempt holds a worker.
- Fairness across Tenants and per-Tenant rate limits are deferred.

## Security

- **Tenant authentication:** API keys stored as hashes, sent as a bearer token (REST) or in metadata (gRPC), each scoped to one Tenant.
- **Signing:** Standard Webhooks with HMAC-SHA256 ([ADR 0003](adr/0003-standard-webhooks-signing.md)). Each Endpoint has its own signing secrets (`whsec_` prefix), returned by the API when the Endpoint is created.
- **Secrets at rest:** AES-GCM, with a key ID stored in each ciphertext and a keyring in config. Deliveries carry the encrypted copy, which the dispatcher decrypts just before signing.
- **Signing-secret rotation** (M3): the new secret is added and the old one expires after 48 h (longer than the retry window). Deliveries are signed with every secret that hasn't expired, and replays use the Endpoint's current secrets.

## Limits and validation

- Payload: JSON, at most **256 KB** (`413` otherwise).
- Event type: `^[a-z0-9_]+(\.[a-z0-9_]+)*$`, at most **128 bytes**. Every Message in a handoff batch carries its Event type, so the cap bounds what each Message adds to a batch.
- Endpoint filter: NATS-style wildcards (`*` matches one token, `>` matches the rest). An empty filter matches everything. At most **32 patterns**, each at most **128 bytes**, which bounds the cost of matching one Endpoint during fan-out on the request path. A pattern may be as long as an Event type, so a filter can name any Event type.
- Application name: not blank, at most **256 bytes**.
- Endpoint URL: an absolute `http` or `https` URL with a host, at most **2048 bytes**, without user information (it would be stored in plain), a fragment (never sent) or unescaped whitespace.
- Handoff batches stay under the NATS maximum message size (1 MB by default).
- In v1, an Endpoint's URL and event filter can't be changed after creation (signing secrets can be rotated).

## API

- Protobuf first ([ADR 0004](adr/0004-protobuf-first-api.md)): `.proto` files with HTTP annotations, from which `buf` generates the gRPC server, the grpc-gateway REST/JSON gateway and an OpenAPI spec.
- The Tenant API is the protobuf package `herald.tenant.v1`, in a buf module of its own (`api/`). `buf breaking` checks it with the FILE rule set, because Tenants depend on more than its encoding: JSON field names through the gateway, gRPC method paths, and the names in clients generated from the protos. No buf rule checks the HTTP annotations; the integration tests pin each operation's REST method and path instead.
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
| Observability | OpenTelemetry traces (context passed in NATS headers), Prometheus metrics |
| Tests | standard `testing`, `go-cmp`, testcontainers-go |

## Testing

- **Unit tests** for pure logic: routing and filters, the retry schedule, signing.
- **Integration tests** with testcontainers-go against real Postgres and NATS. No mocks of either.
- **End-to-end tests** run the full compose stack against a fake receiver that can be scripted to fail or slow down. They prove retries, duplicate protection and isolation.

## CI (GitHub Actions)

On every PR: `golangci-lint`, `go test -race` (unit and integration), `buf lint` and `buf breaking` against `main`, a check that sqlc and protobuf generated code is up to date, and Docker image builds. End-to-end tests run on `main`.

## Milestones

1. **M1: Core delivery path.** Admin CLI (Tenant, API key). Tenant API for Applications, Endpoints and Messages, with idempotency keys. Outbox → NATS → dispatcher. Signed Attempts with retries and the per-Endpoint concurrency cap. Deliveries end up `succeeded` or `failed`. Structured logs. `docker compose up`, plus an end-to-end test.
2. **M2: Delivery API and replay.** Internal gRPC `DeliveryService` through herald-api: status, Attempt history, replay. Retention job.
3. **M3: Rotation.** Signing-secret rotation, and encryption-key rotation through the keyring.
4. **M4: Observability.** OpenTelemetry tracing across api → NATS → dispatcher, Prometheus metrics (handoff lag, Attempt latency, Deliveries by state, how full each Endpoint's concurrency cap is), Jaeger and Prometheus in compose.
5. **M5: Kubernetes.** kind and Kustomize manifests, migrations as a Job, `make kind-up`.

## Deferred

- Changing an Endpoint's URL or filter (needs an "endpoint changed" message from api to dispatcher).
- Fairness across Tenants and per-Tenant rate limits.
- Re-encrypting stored secrets after an encryption-key rotation.
- Tenant self-service and an admin API.
- Ordered delivery.
