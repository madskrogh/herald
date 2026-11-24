# Latency

How long Herald takes to move a Message along and to answer a Tenant API call, normally and during the faults below, and how soon it recovers from a failure. The values are in [limits.md](limits.md), and when an Attempt falls due is in [delivery.md](delivery.md) (R-10, R-11).

## Faults

The faults the latency requirements name. Each is how a part of the stack Herald runs on fails, and how far:

| Fault | What happens | How far |
|---|---|---|
| NATS unavailable | NATS answers nothing, though its connections stay open | for `fault-nats-unavailable` |
| Postgres slowed | Postgres runs with a small share of one CPU | at `fault-postgres-cpu` |
| Dispatcher restarted | a herald-dispatcher is stopped, with an Attempt in flight, and started again | once |

A Message is on its way from the Tenant API call that created it until its last Attempt. A fault affects it when the two overlap.

## Handoff and Attempts

### R-71 · implemented

When no fault affects a Message, Herald MUST start handing it off within `max-delay` of the Tenant API call that created it returning.

Why: a Tenant expects its Message to reach its Endpoints in about the time a request takes, not in a batch some time later. Handoff waits only for the relay's next poll of the Outbox.

### R-72 · implemented

When no fault affects a Message, Herald MUST start each of its Attempts within `max-delay` of when it falls due (R-10, R-11).

Why: the retry schedule is a promise about when Attempts come, and an Attempt that starts late breaks it as surely as one that starts early. Starting an Attempt waits only for a worker's next poll of the Deliveries due.

### R-73 · implemented

Unlike R-71 and R-72, when NATS is unavailable or a dispatcher restarts while a Message is on its way, Herald MUST start handing it off, and start each of its Attempts, within `max-delay-in-fault` of the time R-71 and R-72 measure from.

Why: a fault may hold a Message back, but only while it lasts and while Herald recovers from it, not until something else happens. Each of these faults is over in seconds, so a Message held back longer is stalled, not delayed.

| Fault while the Message is on its way | Handoff starts | Attempt starts |
|---|---|---|
| NATS unavailable for `fault-nats-unavailable` | within `max-delay-in-fault` of the Tenant API call | within `max-delay-in-fault` of falling due |
| Dispatcher restarted | within `max-delay-in-fault` of the Tenant API call | within `max-delay-in-fault` of falling due |
| none | within `max-delay` (R-71) | within `max-delay` (R-72) |

### R-74 · implemented

Unlike R-71 and R-72, when Postgres is slowed while a Message is on its way, Herald SHOULD start handing it off, and start each of its Attempts, within `max-delay-in-fault` of the time R-71 and R-72 measure from.

Why: as for R-73, but how long Herald takes with Postgres at `fault-postgres-cpu` depends on the machine it runs on: most runs stay within a few seconds, and a run on a correct build has taken over 20 s (#80). A MUST would be broken by a correct build now and then, so this is a SHOULD, and the scenario invariants hold Herald to it all the same.

## Recovery

### R-89 · planned

When a dispatcher stops during an Attempt without recording it or giving its Delivery back, as when it is killed, that Delivery MUST be due again within `lease-duration` of the Attempt's start.

Why: a Delivery a dispatcher claimed is held for it alone, so no other dispatcher makes an Attempt at the same time. A dispatcher that dies can't give it back, so the hold has to run out on its own, and `lease-duration` says when. A dispatcher that is told to stop gives its Deliveries back at once (R-99).

### R-90 · planned

Once Postgres and NATS are both reachable again after either was unreachable, Herald MUST resume handing Messages off and starting Attempts within `max-resume-delay`.

Why: Herald retries a failing dependency less often the longer it fails, so a long outage costs it neither log volume nor load on the dependency as it comes back; the wait is capped so that an outage ends for Tenants soon after it ends for Herald (#114).

## Tenant API

### R-91 · planned

When Postgres isn't slowed, every Tenant API call MUST return within `max-api-latency`, measured by the Tenant from sending the call to receiving the response. This holds with no fault, while NATS is unavailable, and while a dispatcher restarts.

Why: a Tenant calls the Tenant API while handling its own users' requests, so a slow call slows the Tenant's product. Accepting a Message needs only Postgres ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)), so NATS and the dispatchers can't slow it (decided in #114).

| Fault during the call | Returns within |
|---|---|
| none | `max-api-latency` |
| NATS unavailable for `fault-nats-unavailable` | `max-api-latency` |
| dispatcher restarted | `max-api-latency` |
| Postgres slowed | `max-api-latency-in-fault` (R-92) |

### R-92 · planned

Unlike R-91, while Postgres is slowed, every Tenant API call SHOULD return within `max-api-latency-in-fault`.

Why: every call waits on Postgres, so how long it takes with Postgres at `fault-postgres-cpu` depends on the machine, as for R-74. The limit catches a call that stalls rather than one that is slow (decided in #114).
