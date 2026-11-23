# Operations

What operators rely on when they run Herald. More of this boundary, such as herald-admin and each service's settings, is to be written as planned requirements (#114).

## Configuration

### R-76 · implemented

When `HERALD_RETRY_SCALE` is set, herald-dispatcher MUST multiply every one of `retry-delays`, and its jitter, by it, and log a warning that the retry schedule is shrunk. A value that isn't a number in `retry-scale` MUST stop the service from starting.

Why: a local stack and the end-to-end tests need a whole retry schedule to run in seconds (`retry-scale`). The warning keeps a shrunk schedule from reaching production unnoticed, and refusing to start on a bad value keeps a typo from running the full schedule, or none.

| `HERALD_RETRY_SCALE` | Failed Attempt | Shortest delay | Longest delay |
|---|---|---|---|
| not set | 1 | 4.5 s | 5.5 s |
| `1` | 7 | 10 h 48 m | 13 h 12 m |
| `0.001` | 1 | 4.5 ms | 5.5 ms |
| `0.001` | 2 | 54 ms | 66 ms |
| `0.001` | 7 | 38.88 s | 47.52 s |
| `0` | n/a | the dispatcher doesn't start | |
| `1.5` | n/a | the dispatcher doesn't start | |
| `fast` | n/a | the dispatcher doesn't start | |

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
