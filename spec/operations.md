# Operations

What operators rely on when they run Herald. More of this boundary, such as herald-admin and each service's settings, is to be written as planned requirements (#114).

## Configuration

### R-76 · implemented

When `HERALD_RETRY_SCALE` is set, herald-dispatcher MUST multiply every one of `retry-delays`, and its jitter, by it.

Why: a local stack and the end-to-end tests need a whole retry schedule to run in seconds (#16). Jitter is scaled with its delay, so a shrunk schedule keeps its shape: unscaled, the jitter on the last delay would be more than an hour at `0.001`.

| `HERALD_RETRY_SCALE` | Failed Attempt | Shortest delay | Longest delay |
|---|---|---|---|
| not set | 1 | 4.5 s | 5.5 s |
| `1` | 7 | 10 h 48 m | 13 h 12 m |
| `0.001` | 1 | 4.5 ms | 5.5 ms |
| `0.001` | 2 | 54 ms | 66 ms |
| `0.001` | 7 | 38.88 s | 47.52 s |

### R-77 · implemented

When `HERALD_RETRY_SCALE` is set to anything but a number in `retry-scale`, herald-dispatcher MUST NOT start.

Why: the setting only shrinks the schedule. Zero or NaN would make every delay zero, so retries come back to back, and a large enough factor overflows the delay (#16). Refusing to start shows the mistake at once, rather than in how Attempts are timed.

| `HERALD_RETRY_SCALE` | herald-dispatcher |
|---|---|
| `0.001` | starts |
| `1` | starts |
| `0` | doesn't start |
| `1.5` | doesn't start |
| `NaN` | doesn't start |
| `fast` | doesn't start |

### R-78 · implemented

When `HERALD_RETRY_SCALE` is less than 1, herald-dispatcher MUST log a warning, as it starts, that the retry schedule is shrunk for local development only.

Why: at `0.001`, the whole schedule runs in about 80 s (#16), so a Delivery fails after a minute's outage. The warning keeps a shrunk schedule from reaching production unnoticed.

| `HERALD_RETRY_SCALE` | Warning |
|---|---|
| `0.001` | logged |
| `1` | none |
| not set | none |

## Rollouts

### R-58 · implemented

A change to the handoff package `herald.handoff.v1` MUST keep its wire encoding compatible with the version before it, so a herald-api and a herald-dispatcher one release apart hand Messages off to each other.

Why: the two services are rolled out one after the other, so for a while each runs beside the other's previous version ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)). The handoff is internal and never served to Tenants, so only its encoding matters, not its names.

| Change to `herald.handoff.v1` | Allowed |
|---|---|
| add a field | yes |
| rename a field, keeping its number | yes |
| change a field's number or type | no |
| remove a field without reserving its number | no |
