# Herald's contract, stated outside the Go code

It should be possible to rewrite Herald in another language and show that the new implementation does what this one does. Today that takes reading the Go: the scenario invariants are Go functions over Go types, the scenario's traffic comes from a seed fed to Go's random number generator, the telemetry schema is Go constants checked by an in-process test, and nothing says which Postgres tables and columns the scenario relies on. Most of the rules are prose in `docs/design.md`, mixed with architecture, rationale, milestones and deferred items, and the invariants are stated three times: there, in `docs/testing.md`, and in Go comments.

So Herald's contract is stated outside the Go code, in files a person reads and reasons about, and the Go code becomes one implementation of it. A check a machine runs is derived from those files, never the other way round.

## The boundary

An implementation must match:

- the Tenant API: the `.proto` files, the OpenAPI spec, and the golden responses;
- the webhook wire format: Standard Webhooks ([ADR 0003](0003-standard-webhooks-signing.md));
- the telemetry schema: span names, attribute keys and types, the allowed values of `herald.outcome`, and the trace shape. It is stated explicitly, enforced against the spans a run produces, and expected to grow;
- the Postgres schema, limited to the columns below.

`herald-admin` is not in the boundary for now.

### Postgres columns

These are what the scenario writes for setup and reads for final state. This table is the column list's first statement; once the column list is a data file, that file is its home.

| Table | Columns | What must hold |
|---|---|---|
| `ingest.tenants` | `id`, `name` | Creating a row with a name creates a Tenant. |
| `ingest.api_keys` | `tenant_id`, `key_hash` | `key_hash` is the SHA-256 of a key that the Tenant API then accepts for that Tenant. |
| `ingest.messages` | `id`, `tenant_id`, `idempotency_key`, `handed_off_at`, `parked_at` | `id` is the Message ID the Tenant API returned, and `tenant_id` is its Tenant's `id`. The two timestamps are read only as set or not set. |
| `delivery.deliveries` | `id`, `message_id`, `endpoint_id`, `state` | `message_id` is the Message ID the Tenant API returned, and `endpoint_id` is the Endpoint ID. `state` is `pending`, `succeeded` or `failed`. |
| `delivery.attempts` | `delivery_id`, `number` | One row per recorded Attempt, numbered from 1. |

Nothing else in either schema is in the contract: not indexes, not the other columns. Behaviour that rests on an index, such as the Idempotency-Key, is specified and tested through the Tenant API. Once the Delivery API (M3) returns Attempt history, `delivery.attempts` leaves the contract.

## Form

- The spec is a set of numbered requirements, one rule each. An exception is its own requirement and names the requirement it limits.
- Requirements use MUST, SHOULD and MAY, and conditional sentence patterns ("When…", "If… then…", "While…").
- Each requirement has a stable ID, a status (implemented or planned), and a short note on why, which links to an ADR where one exists.
- A grammar, such as the Event type and the filter syntax, is written in ABNF.
- Numbers and limits are a table, with the reason beside each one.
- Test vectors are readable tables, each tagged with the requirement it exercises. The checks read the same file a person reads.
- CI fails if a MUST has no vector, or a vector names a requirement that doesn't exist.

## Where each fact lives

Each fact has one home, and everything else links to it.

| Fact | Lives in |
|---|---|
| Behaviour | the spec |
| Words | `GLOSSARY.md` |
| Reasons | the ADRs, which are never edited, only superseded |
| Shapes | the protos, the telemetry schema file, the Postgres column list |
| What Herald is and how it's built | the README |

If the spec and another document disagree about behaviour, the spec wins. `docs/design.md` is split between the spec and the README, and retired.

## Changing the spec

- The spec is edited in place, and git history is its log.
- Requirement IDs are never reused. A removed requirement stays, marked withdrawn, with the reason.
- A behaviour change is one PR: the spec and its vectors first, failing, then the code that makes them pass. Review reads them in that order.
- The issue states the problem and the change in plain words, and names the requirement IDs it touches. The spec text is written only in the PR.
- A decision with a real trade-off is an ADR, in its own PR, first.

## The existing ADRs

Each ADR is one of three kinds. A behavioural ADR is distilled into requirements and stays as their reason. An architecture ADR binds this implementation: a port keeps it or replaces it on purpose. A Go-specific ADR doesn't apply to a port.

| ADR | Kind | Note |
|---|---|---|
| [0001](0001-at-least-once-delivery.md) At-least-once delivery | behavioural | |
| [0002](0002-two-services-outbox-nats-handoff.md) Two services, outbox column, NATS handoff | architecture | `handed_off_at` is in the contract only as the column the scenario reads. |
| [0003](0003-standard-webhooks-signing.md) Standard Webhooks for signing | behavioural | Encrypting secrets at rest, and the keyring, are architecture. |
| [0004](0004-protobuf-first-api.md) Protobuf-first API | architecture | The `.proto` files are in the contract; generating the server and gateway from them is how this build serves them. |
| [0005](0005-sqlc-instead-of-orm.md) sqlc instead of an ORM | Go-specific | |
| [0006](0006-clickhouse-telemetry-store.md) ClickHouse as the telemetry store | architecture | The checks read spans from it, so a port only has to export them through the Collector. |

Checks that only make sense for the Go build stay as repository checks outside the spec. The goroutine-leak invariant is one. The spec gets a resource-leak requirement only if one can be stated without Go.

## Considered Options

- **The Go code as the contract** (today): rejected. A port would have to read the Go to know what to do, and could only show it matches by running Go checks over Go types.
- **RFC-style paragraphs**: rejected on evidence. In a trial, six independent implementations of three rules (no Attempt after a `2xx`, the retry schedule, Event type and filter matching) were written without the Go code, from three spec styles. Both implementations built from paragraphs missed the last sentence of the retry-schedule paragraph (as many Attempts as there are repeats may come early). They failed 2 of 26 hand-picked and 17 of 120 random cases, and neither noticed the gap. All four implementations built from numbered requirements passed every case.
- **Numbered requirements with worked examples**: not required. In the same trial, worked examples showed no extra gain, because every implementation built from numbered requirements already passed without them. The sample is small, and it can't separate one rule per item from the sentence patterns.

## Consequences

- The work after this ADR is separate changes: the spec skeleton (structure, IDs, status field, and the CI link check); splitting `docs/design.md` into the spec and the README; the telemetry schema as a data file, which the Go constants are checked against and a check runs over every span of a scenario run; the Postgres column list as a data file, with each column's type and the IDs the database generates, checked against the migrated database; the scenario invariants as requirements with vectors, which the Go checks read; and the scenario's traffic as data rather than a seed.
- Until the spec exists, `docs/design.md` stays the statement of behaviour.
- The development workflow changes to follow "Changing the spec": today an issue, with the decisions recorded on it, is what a change is checked against. That change is made on its own.
- The checks that read the telemetry schema come after the schema file, so they read it rather than the Go constants.
- The scenario's Go harness reads Postgres through the services' own queries, which touch more columns than the contract lists, and one table it doesn't (`delivery.messages`). It has to read only the listed columns before a port can be checked by it.
- Known gaps, not built now: provenance (which spec version a build passed), and checks against production, which Herald doesn't have.
