# Latency

How long Herald takes to move a Message along, normally and during the faults below. The values are in [limits.md](limits.md), and when an Attempt falls due is in [delivery.md](delivery.md) (R-10, R-11).

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
