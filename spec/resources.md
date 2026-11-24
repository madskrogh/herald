# Resource use

What a service may hold under load and at rest. The load is the scenario ([docs/testing.md](../docs/testing.md)), and every measure is taken from outside the process, so it holds whatever language the service is written in. The values are in [limits.md](limits.md).

A service's memory is its container's memory as the container runtime reports it, without the page cache. Its level before traffic is that memory once it has started and before it handles its first call or Batch; for a service restarted during the scenario, it is the level of the process that started first.

## Memory

### R-84 · planned

While the scenario runs, each service's memory MUST stay below `max-service-memory`.

Why: a service whose memory grows with its traffic runs out of it in production, where the traffic doesn't end. Herald keeps Messages, Deliveries and Attempts in Postgres, not in memory, so what a service holds shouldn't depend on how much it has handled.

### R-85 · planned

`memory-settle-time` after the scenario ends, each service's memory MUST be within `memory-at-rest-margin` of its level before traffic.

Why: Herald keeps no cache, so anything a service still holds once the traffic has stopped is a leak. A cache, if one is ever added, changes this requirement on purpose (decided in #114).

| Service | Before traffic | Peak | `memory-settle-time` after the scenario | Holds |
|---|---|---|---|---|
| herald-api | 5 MiB | 16 MiB | 10 MiB | yes |
| herald-api | 5 MiB | 16 MiB | 16 MiB | no |

## Connections

### R-86 · planned

While the scenario runs, and at any other time, a service process MUST NOT hold more than `max-db-connections` connections to Postgres at once, counted as the open connections from the process to Postgres.

Why: every replica of both services draws on Postgres's one connection limit, so each needs a cap an operator can plan for. Nothing requires idle connections to close after the scenario: a leaked connection shows as the cap being reached (decided in #114).

### R-87 · planned

A herald-dispatcher process MUST NOT have more than `max-attempts-in-flight` Attempts in flight at once, measured as the requests from it that Endpoints have open at the same moment, whatever the load.

Why: each Attempt in flight holds a connection to an Endpoint, so the cap bounds the connections and the memory one dispatcher uses however many Deliveries are due. More throughput comes from more replicas.

### R-88 · planned

herald-dispatcher MAY keep a connection to an Endpoint open between Attempts, but one dispatcher MUST NOT keep more of them open to one Endpoint than `max-in-flight-per-endpoint`. How long it keeps an idle one open MAY be any time.

Why: reusing a connection saves the Endpoint a TLS handshake per Attempt, and an Endpoint never has more Attempts in flight than that cap (R-19), so more idle connections would only hold its resources.

### R-108 · planned

herald-api MUST close a REST connection whose client hasn't sent a request's headers within `rest-header-timeout`, or the whole request within `rest-read-timeout`, of starting it, and MUST close a REST connection that has been idle for `api-idle-timeout`.

Why: each open connection holds memory and a file descriptor, so a client that sends slowly, or opens connections and leaves them, must not be able to hold them for ever (#114).

### R-116 · planned

herald-api MUST close a gRPC connection whose client hasn't finished setting it up within `grpc-handshake-timeout` of connecting, and MUST close a gRPC connection that has had no call in progress for `api-idle-timeout`.

Why: as for R-108. The two transports serve the same Tenant API, so a client mustn't be able to hold connections on one that the other would close (#114).
