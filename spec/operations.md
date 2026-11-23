# Operations

What operators rely on when they run Herald. More of this boundary, such as herald-admin and each service's settings, is to be written as planned requirements (#114).

## Rollouts

### R-58 · implemented

A herald-api and a herald-dispatcher one release apart MUST be able to hand Messages off to each other. A change to the handoff package `herald.handoff.v1` MUST keep its wire encoding compatible.

Why: the two services are rolled out one after the other, so for a while each runs beside the other's previous version ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)). The handoff is internal and never served to Tenants, so only its encoding matters, not its names.

| Change to `herald.handoff.v1` | Allowed |
|---|---|
| add a field | yes |
| rename a field, keeping its number | yes |
| change a field's number or type | no |
| remove a field without reserving its number | no |
