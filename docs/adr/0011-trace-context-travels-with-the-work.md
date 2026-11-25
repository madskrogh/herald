# Trace context travels with the work

One Message's whole journey is one trace: from the Tenant API call, through the Outbox and the handoff, to every Attempt. But between those steps the work waits in Postgres, where a request's context is lost. A relay claims a Message later, and a worker makes an Attempt seconds or up to a day later, possibly on another replica and after a restart.

So the work carries its own trace context:

- The Message row keeps the trace context (W3C `traceparent`) it was created under, and the relay continues the trace from it.
- A Batch holds Messages from many traces, so each Message carries its own context in the Batch. The Batch is a trace of its own, linked to each Message it carries.
- Each Delivery row keeps the context it was stored under, and every Attempt for it continues the trace from the row, whenever and wherever it runs.

## Considered Options

- **Context in the headers of the NATS message that carries a Batch**: rejected. There is one set of headers per Batch, and a Batch holds Messages from many traces.
- **A span for each Delivery, covering its Attempts**: rejected. It would stay open for up to a day and across restarts.

## Consequences

- Kind, in [ADR 0007](0007-code-regenerated-from-the-spec.md)'s terms: architecture.
- Each schema has a column for the context, and the handoff contract a field. Removing either loses the context of the rows or Batches that carry it, and their later spans start traces of their own.
- Spans keep joining a Message's trace for as long as its retry schedule runs, so anything that groups or keeps spans by trace must allow for that.
- A replay doesn't join the Message's trace. The replay call starts a trace of its own, as the create Message call does, and the replay's Attempts continue it. That trace links to the Message's trace. Joining would keep the Message's trace taking spans for as long as `retention`, not just for the retry schedule.
- The trace's shape and what each span carries are debugging data, so they belong in the spec. This ADR is the architecture that lets that shape survive the Outbox, the handoff and the time between Attempts.
