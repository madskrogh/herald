# Limits

Every number and limit the spec's requirements name, with the reason for its value. A requirement names a limit by its name instead of repeating the value.

| Limit | Value | Reason |
|-------|-------|--------|
| `attempt-timeout` | 15 s | Long enough for a slow handler that does its work before answering, and it caps how long one Endpoint holds a worker. |
| `max-attempts` | 8 | With `retry-delays`, Attempts span about a day. |
| `retry-delays` | 5 s, 1 m, 10 m, 1 h, 3 h, 6 h, 12 h: the delay after each failed Attempt, from the 1st to the 7th | The early delays are short, so a brief failure recovers in seconds; the later ones are long, so an Endpoint that is down for up to a day (about 22 h in all) still gets its webhooks. |
| `retry-jitter` | 10% either way | Keeps Deliveries that failed together from all retrying at the same moment. |
| `max-in-flight-per-endpoint` | 4 | Limits the load Herald puts on a slow Endpoint, and how many of a dispatcher's workers one Endpoint can take. |
| `response-body-kept` | 4 KiB | Enough for an error message, and it bounds the storage each Attempt takes. |
| `retention` | 30 days | A month of history is enough to inspect and replay Deliveries, and it bounds storage. |
| `secret-overlap` | 48 h | Longer than a Delivery's retry schedule, so a Delivery that carries only the old secret still verifies. |
| `idempotency-window` | 24 h | A client retries a failed request within hours, not days. |
| `idempotency-key-bytes` | 256 | A key needs a cap, to stay within what the database's unique index holds; 256 is a round number well inside it. |
| `max-payload` | 256 KiB | A webhook is a notification, not a way to move bulk data. |
| `max-request-body` | 1600 KiB | Six times `max-payload`, since each payload byte may be escaped as `\u00XX` in the JSON string that carries it, plus 64 KiB for the other fields. |
| `event-type-bytes` | 128 | An Event type needs a cap, since every Delivery handed off carries one; 128 is a round number with headroom. |
| `filter-pattern-bytes` | 128 | Equal to `event-type-bytes`, so a filter can name every Event type. |
| `max-filter-patterns` | 32 | A filter needs a cap, since it is matched on the request path; 32 is a round number with headroom. |
| `max-endpoints-per-application` | 100 | A Message's Deliveries are handed off together, in one NATS message of at most 1 MB, and each carries its Endpoint's URL and signing secrets. With a 256 KiB payload, about 350 would fill it; 100 leaves a margin. |
| `endpoint-url-bytes` | 2048 | The length most clients and servers accept. |
| `application-name-bytes` | 256 | A name needs a cap; 256 is a round number with headroom. |
| `max-page-size` | 100 | Bounds the size of one list response. |
