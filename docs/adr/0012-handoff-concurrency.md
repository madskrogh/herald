# Handoff concurrency: one relay per herald-api process

Each herald-api process runs one relay. A relay sends one Batch at a time and waits for its reply before the next, and a dispatcher stores one Batch at a time. So handoff stores at most as many Batches at once as there are herald-api replicas, and dispatchers beyond that number add no handoff throughput.

Nothing has shown that handoff throughput limits Herald, and the load test is where it would show. Until it does, one relay per process keeps handoff's cost fixed: each relay holds a pooled database connection, and its claimed Messages' locks, for up to the request timeout while it waits for a reply.

## Considered Options

- **Several relays per process, behind a setting**: deferred until the load test shows handoff lag growing because the relays can't keep up ([#124](https://github.com/madskrogh/herald/issues/124)). The connection pool must grow with the number of relays, and there must be at least as many dispatchers as relays across all replicas.
- **A limit that adapts to reply latency**: deferred, for the same reason.

## Consequences

- Kind, in [ADR 0007](0007-code-regenerated-from-the-spec.md)'s terms: architecture.
- Handoff throughput grows with herald-api replicas, not with dispatchers.
- Handoff lag under load belongs in the spec as a latency requirement. Running more relays per process is a new ADR, which replaces this one.
