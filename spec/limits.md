# Limits

Every number and limit the spec's requirements name, with what it limits and the reason for its value. A requirement names a limit by its name instead of repeating the value.

| Limit | What it limits | Value | Reason |
|-------|----------------|-------|--------|
| `attempt-timeout` | How long an Endpoint has to answer an Attempt, from the Attempt's start | 15 s | Long enough for a slow handler that does its work before answering, and it caps how long one Endpoint holds a worker. |
| `max-attempts` | How many Attempts Herald makes for one Delivery before it fails | 8 | With `retry-delays`, Attempts span about a day. |
| `retry-delays` | The delay before each retry of a failed Attempt, after the 1st to the 7th Attempt fails | 5 s, 1 m, 10 m, 1 h, 3 h, 6 h, 12 h | The early delays are short, so a brief failure recovers in seconds; the later ones are long, so an Endpoint that is down for up to a day (about 22 h in all) still gets its Messages. |
| `retry-jitter` | How far each of `retry-delays` may vary | 10% either way | Keeps Deliveries that failed together from all retrying at the same moment. |
| `retry-scale` | How far `HERALD_RETRY_SCALE` may shrink every one of `retry-delays`, and its jitter | a factor in (0, 1]; 1 when not set | A local stack and the end-to-end tests need a failing Endpoint retried, and given up on, in seconds rather than a day. A factor above 1 would only stretch a schedule that already spans a day. |
| `max-delay` | How long a Message may wait for its handoff, and an Attempt to start once due, when no fault affects it | 2 s | The relay and the workers each poll every 500 ms, so a Message or an Attempt waits for at most one or two polls; 2 s leaves room for a busy machine. One run measured at most 0.5 s (#80). |
| `max-delay-in-fault` | The same, while NATS is unavailable, Postgres is slowed or a dispatcher restarts | 15 s | Loose enough that only a stall breaks it, not the fault itself: one run measured up to 5.9 s under a fault (#80). Not one limit per fault, since timing under a slowed Postgres depends on the machine. |
| `fault-nats-unavailable` | How long NATS is unavailable in the fault R-73 names | 5 s | Long enough that the relay tries to hand off and fails at least once, short enough that the Outbox recovers well within `max-delay-in-fault`. |
| `fault-postgres-cpu` | The share of one CPU Postgres has in the fault R-74 names | 1% | Little enough that a Delivery's first Attempt comes seconds late, so the fault shows; a tenth of a CPU barely shows. |
| `max-in-flight-per-endpoint` | How many Attempts to one Endpoint may be in flight at once, across all dispatchers | 4 | Limits the load Herald puts on a slow Endpoint, and how many of a dispatcher's workers one Endpoint can take. |
| `response-body-kept` | How much of an Endpoint's response body Herald keeps with each Attempt | 4 KiB | Enough for an error message, and it bounds the storage each Attempt takes. |
| `retention` | How long Herald keeps a Delivery and its Attempts | 30 days | A month of history is enough to inspect and replay Deliveries, and it bounds storage. |
| `secret-overlap` | How long an Endpoint's previous signing secrets stay valid after a rotation | 48 h | Longer than a Delivery's retry schedule, so a Delivery that carries only the old secret still verifies. |
| `idempotency-window` | How long an Idempotency-Key holds the Message it was first sent with | 24 h | A client retries a failed request within hours, not days. |
| `idempotency-key-bytes` | The longest Idempotency-Key | 256 | A key needs a cap, to stay within what the database's unique index holds; 256 is a round number well inside it. |
| `max-payload` | The largest Message payload | 256 KiB | A Message is a notification, not a way to move bulk data. |
| `max-request-body` | The largest request body over REST | 1600 KiB | Six times `max-payload`, since each payload byte may be escaped as `\u00XX` in the JSON string that carries it, plus 64 KiB for the other fields. |
| `event-type-bytes` | The longest Event type | 128 | An Event type needs a cap, since every Delivery handed off carries one; 128 is a round number with headroom. |
| `filter-pattern-bytes` | The longest pattern in an Endpoint's filter | 128 | Equal to `event-type-bytes`, so a filter can name every Event type. |
| `max-filter-patterns` | How many patterns an Endpoint's filter may hold | 32 | A filter needs a cap, since it is matched on the request path; 32 is a round number with headroom. |
| `max-endpoints-per-application` | How many Endpoints one Application may have | 100 | A Message's Deliveries are handed off together, in one NATS message of at most 1 MB ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)), and each carries its Endpoint's URL and signing secrets. With a 256 KiB payload, about 350 would fill it; 100 leaves a margin. |
| `signing-secret-bytes` | The size of the random key in a signing secret | 24 | The smallest key Standard Webhooks allows. |
| `endpoint-url-bytes` | The longest Endpoint URL | 2048 | The length most clients and servers accept. |
| `application-name-bytes` | The longest Application name | 256 | A name needs a cap; 256 is a round number with headroom. |
| `max-page-size` | The most results one page of a list call returns | 100 | Bounds the size of one list response. |
