# Two services, outbox column, NATS request-reply handoff

Herald is split into `herald-api` (accepts Messages, owns Tenants, Applications and Endpoints) and `herald-dispatcher` (executes Deliveries and retries). They share one Postgres instance but own separate schemas (`ingest`, `delivery`), and neither reads the other's schema. herald-api works out a Message's Deliveries when it accepts the Message. The marker for "not yet handed off" is a `handed_off_at` column on the messages table, written in the same transaction as the Message: the outbox pattern, without a separate outbox table.

A relay in herald-api sends batches of Deliveries to the dispatchers as plain NATS request-reply to a queue group. A dispatcher replies only after it has committed the Deliveries to `delivery`, and only then does the relay set `handed_off_at`. The outbox is therefore the only durable queue: when the dispatchers are down, the backlog builds up in one place, Postgres, where it can be queried directly.

## Considered Options

- **JetStream work queue with file storage**: rejected. JetStream's publish ack means "stored in the stream", not "stored by the dispatcher". That adds a second durable queue that has to be run and monitored, where work can pile up away from Postgres, without adding any guarantee the outbox doesn't already give.
- **Synchronous gRPC handoff**: rejected. Long-lived HTTP/2 connections defeat Kubernetes Service load balancing. NATS queue groups spread requests across dispatchers natively.
- **Kafka**: rejected. The handoff is a work queue, not a partitioned log.
- **A separate outbox table**: deferred. A column is enough while there is only one kind of handoff.
- **Shared schema / separate database instances**: rejected. A shared schema erases the service boundary, and separate instances add operational cost with no benefit at this scale.

## Consequences

- A lost reply causes a resend. The dispatcher inserts with `ON CONFLICT DO NOTHING`, so resends are harmless.
- Several herald-api replicas can each run a relay. Each claims outbox rows with `FOR UPDATE SKIP LOCKED`.
- Batches must stay under the NATS maximum message size (1 MB by default). A Message's payload is sent once per batch, not once per Delivery.
- Deliveries carry a copy of their Endpoint's details (except signing secrets during a rotation; see ADR 0003), so in v1 an Endpoint's URL and event filter cannot be changed after creation.
- Updating `handed_off_at` adds a new row version and entries in every index for each Message (the column is in a partial index, which rules out in-place "HOT" updates). This is acceptable at the expected volume.
