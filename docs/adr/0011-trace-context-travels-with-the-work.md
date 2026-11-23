# Trace context travels with the work

One Message's whole journey is one trace: from the Tenant API call, through the Outbox and the handoff, to every Attempt. But between those steps the work waits in Postgres, where a request's context is lost. A relay claims a Message later, and a worker makes an Attempt seconds or up to a day later, possibly on another replica and after a restart.

So the work carries its own trace context:

- The Message row keeps the trace context (W3C `traceparent`) it was created under, and the relay continues the trace from it.
- A Batch holds Messages from many traces, so each Message carries its own context in the Batch. The Batch is a trace of its own, linked to each Message it carries.
- Each Delivery row keeps the context it was stored under, and every Attempt for it continues the trace from the row, whenever and wherever it runs.

## Considered Options

- **Context in the handoff message's headers**: rejected. There is one set of headers per Batch, and a Batch holds Messages from many traces.
- **A span for each Delivery, covering its Attempts**: rejected. It would stay open for up to a day and across restarts.

## Consequences

- Each schema has a column for the context, and the handoff contract a field. Neither can be taken back once rows are stored with them.
- A trace stays open as long as retries go on, about a day.
- Whether a replayed Delivery's Attempts join the original trace is decided with replay (M3).
- The trace's shape and what each span carries are debugging data, stated in the spec ([ADR 0007](0007-code-regenerated-from-the-spec.md)). This ADR is the architecture that lets that shape survive the Outbox, the handoff and the time between Attempts.
