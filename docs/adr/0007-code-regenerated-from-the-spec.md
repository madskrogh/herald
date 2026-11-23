# Herald's code can be regenerated from its spec

Every decision in Herald that someone at one of its boundaries can notice is stated outside the code, so that all of Herald's code could be regenerated from those statements. That means what Herald returns and sends for what it is given, what it does in each case, how long it takes, what resources it uses, and what debugging data it leaves for the developers who run it. The aim isn't to change language. It is to force every decision out of the code and into a statement a person can read, question, and change on purpose.

Today many decisions exist only in the code. The scenario invariants are Go functions over Go types, and the limits they hold Herald to (handoff lag, how late an Attempt may start) are Go constants. The scenario's traffic comes from a seed fed to Go's random number generator. The telemetry schema is Go constants checked by an in-process test. Nothing says which Postgres tables and columns the scenario relies on. Most of the rules are prose in `docs/design.md`, mixed with architecture, rationale, milestones and deferred items, and the invariants are stated three times: there, in `docs/testing.md`, and in Go comments.

So the code is derived from the spec, never the other way round, and so is every check a machine runs. The test of the spec: if the code were deleted and regenerated from the spec, the shape files, `GLOSSARY.md` and the ADRs, any difference someone at a boundary could notice is a requirement missing from the spec.

## The boundary

The spec states, at each boundary:

- **What Herald gives for what it is given.** The Tenant API: the `.proto` files, the OpenAPI spec, and the golden responses. The webhook wire format: Standard Webhooks ([ADR 0003](0003-standard-webhooks-signing.md)). What Herald stores, limited to the Postgres columns below.
- **What Herald does in each case.** Every feature, and what happens when an Endpoint fails, a service restarts, a limit is reached or a request is refused.
- **How long it takes.** Latency as numbers with reasons, such as handoff lag and how late an Attempt may start, normally and during each fault the spec names.
- **What resources it uses.** What a service may hold under load and at rest, stated so it can be measured from outside the process, whatever language the code is in. Each such requirement names the load, the measure and the limit: for example, that memory and open connections return to their idle level once the scenario ends.
- **What debugging data it leaves.** The telemetry schema (span names, attribute keys and types, the allowed values of `herald.outcome`, and the trace shape), the logs, and the profiles. A developer investigating a problem must find the same data in regenerated code.
- **What operators use.** `herald-admin`, configuration, and how Herald is deployed and started.

Most of this isn't stated yet. It enters the spec as planned requirements, and the boundary grows as they are written. Until a boundary is stated, the code is its only record, and a regeneration would lose it.

### Decisions found in the code

A decision that exists only in the code is a gap in the spec. Whoever finds one states it: as a requirement with its reason, or with a MAY when any choice is acceptable. They don't leave it in the code.

Moving a fact out of the code is reviewing it as a decision, not copying it. When a follow-up turns constants, migrations or checks into spec, data files or vectors, each value gets its reason, and a value with no reason is questioned before it is written down. A check that compares the code with a file is only as good as the file.

A change in the behaviour comparison ([docs/testing.md](../testing.md)) that no requirement explains is also such a gap: its baseline records what Herald does, and the spec says what it must do.

### Postgres columns

These are what the scenario writes for setup and reads for final state. This table is the column list's first statement; once the column list is a data file, that file is its home.

| Table | Columns | What must hold |
|---|---|---|
| `ingest.tenants` | `id`, `name` | Creating a row with a name creates a Tenant. |
| `ingest.api_keys` | `tenant_id`, `key_hash` | `key_hash` is the SHA-256 of a key that the Tenant API then accepts for that Tenant. |
| `ingest.messages` | `id`, `tenant_id`, `idempotency_key`, `handed_off_at`, `parked_at` | `id` is the Message ID the Tenant API returned, and `tenant_id` is its Tenant's `id`. The two timestamps are read only as set or not set. |
| `delivery.deliveries` | `id`, `message_id`, `endpoint_id`, `state` | `message_id` is the Message ID the Tenant API returned, and `endpoint_id` is the Endpoint ID. `state` is `pending`, `succeeded` or `failed`. |
| `delivery.attempts` | `delivery_id`, `number` | One row per recorded Attempt, numbered from 1. |

Nothing else in either schema is in the spec: not indexes, not the other columns. Behaviour that rests on an index, such as the Idempotency-Key, is specified and tested through the Tenant API. Once the Delivery API (M3) returns Attempt history, `delivery.attempts` leaves the spec.

## The harness

The checks that run the scenario may be written in any language, but they never import the code they check. What they expect, such as which Endpoints a Message reaches and the earliest each Attempt may come, comes from the spec: from its vectors, or from the harness's own code, built from the requirements rather than copied from Herald's code, and checked against the vectors. Otherwise a check grades the code by its own logic, and a bug there becomes the expected result. Anything the harness expects that no requirement states is a gap in the spec.

## Form

- The spec is a set of numbered requirements, one rule each. An exception is its own requirement and names the requirement it limits.
- Requirements use MUST, SHOULD and MAY, and conditional sentence patterns ("When…", "If… then…", "While…").
- Each requirement has a stable ID, a status (implemented or planned), and a short note on why, which links to an ADR where one exists.
- A grammar, such as the Event type and the filter syntax, is written in ABNF.
- Numbers and limits, including latency and resource limits, are a table, with the reason beside each one.
- Test vectors are readable tables, each tagged with the requirement it exercises. The checks read the same file a person reads.
- CI fails if an implemented MUST has no vector, or a vector names a requirement that doesn't exist. A planned requirement needs no vector, so it can be written down before anyone knows how to test it.

## Where each fact lives

Each fact has one home, and everything else links to it.

| Fact | Lives in |
|---|---|
| Behaviour, latency, resource use, logs, profiles, what operators use | the spec |
| Words | `GLOSSARY.md` |
| Reasons, and the architecture | the ADRs, which are never edited, only superseded |
| Shapes | the protos and the OpenAPI spec generated from them, the golden responses, the telemetry schema file, the Postgres column list |
| What Herald is and how to run it | the README |

The spec, the shapes, `GLOSSARY.md` and the ADRs are what the code is regenerated from. The README describes; it isn't an input, so anything in it a regeneration would need belongs in the spec or an ADR. If the spec and another document disagree about behaviour, the spec wins. `docs/design.md` is split between the spec and the README, and retired.

## Changing the spec

- The spec is edited in place, and git history is its log.
- Requirement IDs are never reused. A removed requirement stays, marked withdrawn, with the reason.
- A behaviour change is one PR: the spec and its vectors first, failing, then the code that makes them pass. Review reads them in that order.
- The issue states the problem and the change in plain words, and names the requirement IDs it touches. The spec text is written only in the PR.
- A decision with a real trade-off is an ADR, in its own PR, first.

## The existing ADRs

Each ADR is one of three kinds:

- **Behavioural:** distilled into requirements, and stays as their reason.
- **Architecture:** regenerated code keeps it unless a new ADR replaces it. What it makes noticeable at a boundary, such as latency, resource use, or where an operator finds a backlog, is stated as requirements.
- **Implementation choice:** regenerated code may choose differently, as long as the requirements hold.

| ADR | Kind | Note |
|---|---|---|
| [0001](0001-at-least-once-delivery.md) At-least-once delivery | behavioural | |
| [0002](0002-two-services-outbox-nats-handoff.md) Two services, outbox column, NATS handoff | architecture | `handed_off_at` is in the spec only as a column the scenario reads. |
| [0003](0003-standard-webhooks-signing.md) Standard Webhooks for signing | behavioural | Encrypting secrets at rest, and the keyring, are architecture. |
| [0004](0004-protobuf-first-api.md) Protobuf-first API | architecture | The `.proto` files are in the spec; generating the server and gateway from them is how the code serves them. |
| [0005](0005-sqlc-instead-of-orm.md) sqlc instead of an ORM | implementation choice | |
| [0006](0006-clickhouse-telemetry-store.md) ClickHouse as the telemetry store | architecture | The checks read spans from it, so the code only has to export them through the Collector. |

A check that only makes sense for one language, such as the goroutine-leak invariant, isn't a requirement. It is one way this code shows it meets a resource requirement stated without the language, and it stays as a repository check that serves that requirement.

## Considered Options

- **The code as the record of its decisions** (today): rejected. A decision in code doesn't say whether it was intended or an accident, so nobody can review it as a decision, and changing it shows only as a diff in the code. Checking that code does the right thing then means checks written over its own types.
- **RFC-style paragraphs**: rejected on evidence. In a trial, six independent implementations of three rules (no Attempt after a `2xx`, the retry schedule, Event type and filter matching) were generated from three spec styles, without the existing code. Both implementations built from paragraphs missed the last sentence of the retry-schedule paragraph (as many Attempts as there are repeats may come early). Each failed 2 of 26 hand-picked and 17 of 120 random retry-schedule cases, and neither noticed the gap. All four implementations built from numbered requirements passed every case.
- **Numbered requirements with worked examples**: not required. In the same trial, worked examples showed no extra gain, because every implementation built from numbered requirements already passed without them. The sample is small, and it can't separate one rule per item from the sentence patterns.

## Consequences

- The work after this ADR is separate changes: the spec skeleton (structure, IDs, status field, and the CI link check); splitting `docs/design.md` into the spec and the README; the telemetry schema as a data file, which the Go constants are checked against and a check runs over every span of a scenario run; the Postgres column list as a data file, with each column's type and the IDs the database generates, checked against the migrated database; the scenario invariants as requirements with vectors, which the Go checks read, with their latency limits moved into the spec's limits table; the scenario's traffic as data rather than a seed; and the resource-use, latency, debugging-data and operator boundaries as planned requirements. Each of them follows "Decisions found in the code".
- Until the spec exists, `docs/design.md` stays the statement of behaviour.
- The development workflow changes to follow "Changing the spec" and "Decisions found in the code": today an issue, with the decisions recorded on it, is what a change is checked against. That change is made on its own.
- The checks that read the telemetry schema come after the schema file, so they read it rather than the Go constants.
- The scenario's harness imports Herald's own code today: Event type and filter matching, the retry schedule, the telemetry constants, and the stores it reads Postgres through. Those stores touch more columns than the spec lists, and one table it doesn't (`delivery.messages`). Each import goes in the follow-up that covers its fact: the telemetry constants with the schema file, the stores with the column list, and filter matching and the retry schedule with the scenario invariants. The harness then has its own matcher and schedule, built from the requirements and checked against the vectors.
- Fault injection restarts and pauses services by name (herald-dispatcher, NATS, Postgres), so it assumes [ADR 0002](0002-two-services-outbox-nats-handoff.md)'s architecture. A new ADR that replaces that architecture also needs a way to inject the same faults.
- Known gaps, not built now: provenance (which spec version a build passed); and checks against production, which Herald doesn't have.
