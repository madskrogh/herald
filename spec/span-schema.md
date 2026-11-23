# Span schema

The spans Herald emits: their names, the trace shape, the attribute keys and their types, and the values of `herald.outcome` ([R-53](observability.md#r-53--implemented) to [R-73](observability.md#r-73--implemented)). Herald's code is checked against this file, and the checks read it to check the spans of a run.

Each span is one wide span: it carries everything known at that point, so a question nobody planned for is a new query, not new instrumentation. That is the reason every attribute appears on every span that knows it, and the reason this file gives for an attribute is why someone queries it.

## Spans

`Parent` lists the spans that may be a span's parent, in its trace. `Root` says whether the span may have no parent: `always`, `may` or `never`. `Links to` lists the spans it may link to.

| Span | Service | Parent | Root | Links to | Why |
|---|---|---|---|---|---|
| `tenant_api.call` | herald-api | | always | | One call to the Tenant API, over gRPC or REST, is the root of the trace of any Message it creates ([R-51](observability.md#r-51--implemented)). It ignores trace context a Tenant sends, so a Tenant can't choose Herald's trace IDs or turn its sampling off. |
| `handoff.batch` | herald-api | | always | `handoff` | A Batch holds Messages from many traces, so it can't have one parent; it starts a trace of its own and links to the handoff of each Message in it ([R-52](observability.md#r-52--implemented)). |
| `handoff` | herald-api | `tenant_api.call` | may | | One Message in a Batch, in the Message's trace. A Message stored before Messages kept their trace context has none to continue, and starts a trace of its own. |
| `store_deliveries` | herald-dispatcher | `handoff` | may | | A dispatcher storing one Message's Deliveries from a Batch, in the Message's trace. Each Delivery keeps its context, so that every Attempt for it is its child. A Message handed off without trace context starts a trace of its own. |
| `attempt` | herald-dispatcher | `store_deliveries` | may | | One Attempt, a child of the span that stored its Delivery, however much later and on whichever dispatcher it runs. A Delivery stored before Deliveries kept their trace context starts a trace of its own for each Attempt. |

## Attributes

Every value is text in the store, where checks and people read it ([ADR 0006](../docs/adr/0006-clickhouse-telemetry-store.md)). A `string` is any text. An `int` is a base-10 integer, emitted as a 64-bit integer. A `uuid` is a UUID in lowercase canonical text form, such as `0198a1b2-0000-7000-8000-0000000000aa`. `Empty` says whether the attribute may be empty text, its zero value where it doesn't apply ([R-53](observability.md#r-53--implemented)): `allowed` or `never`.

| Key | Type | Empty | Why |
|---|---|---|---|
| `rpc.method` | string | never | The Tenant API method called, as a full gRPC method name, such as `/herald.tenant.v1.MessageService/CreateMessage`, so calls are counted and timed by method. |
| `herald.tenant_id` | uuid | allowed | The Tenant whose work it is, so a query reads one Tenant's spans. Empty on a call that isn't authenticated, whose Tenant is unknown. |
| `herald.application_id` | uuid | allowed | The Application the Message was sent to. Empty for a Message stored by a dispatcher before Messages carried their Application across the handoff. |
| `herald.endpoint_id` | uuid | never | The Endpoint an Attempt went to, so a query reads one Endpoint's Attempts. |
| `herald.message_id` | uuid | never | The Message, so a query finds its trace from the ID the Tenant API returned. |
| `herald.delivery_id` | uuid | never | The Delivery an Attempt was for. |
| `herald.attempt` | int | never | Which of its Delivery's Attempts this is, counted from 1, so retries are told from first Attempts. |
| `herald.outcome` | string | never | How the span's work ended. Its values are listed for each span below, so Attempts and handoffs are counted by outcome. |
| `herald.status_code` | int | never | An Attempt's response status, or 0 if there was no response. |
| `herald.latency_ms` | int | never | How long an Attempt's request took, up to the response headers or the error, in milliseconds: the Attempt latency the behaviour comparison reads. It leaves out recording the Attempt, which the span's duration includes. |
| `herald.lateness_ms` | int | never | How long after its Delivery fell due an Attempt started, in milliseconds, so a query sees Attempts that start late. |
| `herald.batch_size` | int | never | How many Messages the Batch holds, so handoff and storing are read against Batch size. |
| `herald.delivery_count` | int | never | How many Deliveries the Message has, its fan-out, so handoff and storing are read against it. |
| `herald.error` | string | allowed | Why the work failed, or empty if it didn't, so a failure is read from its span. On an Attempt it never holds the Endpoint's URL ([R-74](observability.md#r-74--implemented)). |

## Attributes of each span

A span carries exactly these attributes, each once.

| Span | Attributes | Why |
|---|---|---|
| `tenant_api.call` | `rpc.method`, `herald.tenant_id`, `herald.outcome`, `herald.error` | All a call knows before the method runs: the method and the Tenant who called it. |
| `handoff.batch` | `herald.batch_size`, `herald.outcome`, `herald.error` | A Batch holds Messages of many Tenants, so it carries none of their IDs; its links lead to them. |
| `handoff` | `herald.tenant_id`, `herald.application_id`, `herald.message_id`, `herald.batch_size`, `herald.delivery_count`, `herald.outcome`, `herald.error` | All the relay knows of a Message and its Batch. |
| `store_deliveries` | `herald.tenant_id`, `herald.application_id`, `herald.message_id`, `herald.batch_size`, `herald.delivery_count`, `herald.outcome`, `herald.error` | All the dispatcher knows of a Message and its Batch when it stores the Deliveries. |
| `attempt` | `herald.tenant_id`, `herald.application_id`, `herald.endpoint_id`, `herald.message_id`, `herald.delivery_id`, `herald.attempt`, `herald.outcome`, `herald.status_code`, `herald.latency_ms`, `herald.lateness_ms`, `herald.error` | All a worker knows of an Attempt, its Delivery and its Message. |

## Outcomes

The values `herald.outcome` takes on each span.

| Span | `herald.outcome` | Why |
|---|---|---|
| `tenant_api.call` | `OK` | The call succeeded. A call's outcome is its gRPC status code, by grpc-go's name for it, so calls are counted by how the Tenant API answered. |
| `tenant_api.call` | `Canceled` | The call's gRPC status code. |
| `tenant_api.call` | `Unknown` | The call's gRPC status code. |
| `tenant_api.call` | `InvalidArgument` | The call's gRPC status code. |
| `tenant_api.call` | `DeadlineExceeded` | The call's gRPC status code. |
| `tenant_api.call` | `NotFound` | The call's gRPC status code. |
| `tenant_api.call` | `AlreadyExists` | The call's gRPC status code. |
| `tenant_api.call` | `PermissionDenied` | The call's gRPC status code. |
| `tenant_api.call` | `ResourceExhausted` | The call's gRPC status code. |
| `tenant_api.call` | `FailedPrecondition` | The call's gRPC status code. |
| `tenant_api.call` | `Aborted` | The call's gRPC status code. |
| `tenant_api.call` | `OutOfRange` | The call's gRPC status code. |
| `tenant_api.call` | `Unimplemented` | The call's gRPC status code. |
| `tenant_api.call` | `Internal` | The call's gRPC status code. |
| `tenant_api.call` | `Unavailable` | The call's gRPC status code. |
| `tenant_api.call` | `DataLoss` | The call's gRPC status code. |
| `tenant_api.call` | `Unauthenticated` | The call's gRPC status code. |
| `handoff.batch` | `handed_off` | The Batch's round committed: each of its Messages was handed off or parked, and their own spans say which. |
| `handoff.batch` | `failed` | The Batch's round failed, and the error says why. |
| `handoff` | `handed_off` | The Message was handed off. |
| `handoff` | `parked` | The Message was parked, and the error says why. |
| `handoff` | `failed` | The round failed, and the Message stays in the Outbox. |
| `store_deliveries` | `stored` | The Message's Deliveries were stored. |
| `store_deliveries` | `failed` | The Batch was not stored, and the error says why. |
| `attempt` | `succeeded` | The Delivery's state after the Attempt, so Attempts are counted by what they did to their Delivery. |
| `attempt` | `pending` | The Delivery's state after the Attempt: it will be retried. |
| `attempt` | `failed` | The Delivery's state after the Attempt: its retry schedule ran out. |
| `attempt` | `cut_off` | Shutdown cut the Attempt off, and it isn't counted. |
| `attempt` | `not_recorded` | The Attempt couldn't be recorded, and the error says why. |
