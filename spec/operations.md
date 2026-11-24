# Operations

What operators rely on when they run Herald: each service's settings, how Herald is deployed, started and stopped, and herald-admin.

## Configuration

Each service reads its settings from the environment, and nothing else. The settings, and the requirements that say what each does:

| Setting | Read by | Default | What it does |
|---|---|---|---|
| `HERALD_DATABASE_URL` | herald-api, herald-dispatcher, herald-admin | required | The Postgres connection URL (R-95) |
| `HERALD_KEYRING` | herald-api, herald-dispatcher | required to run the service | The keys that encrypt signing secrets at rest (R-97, R-98) |
| `HERALD_NATS_URL` | herald-api, herald-dispatcher | required to run the service | The NATS server URL (R-95) |
| `HERALD_GRPC_ADDR` | herald-api | `:9090` | The address the Tenant API serves gRPC on (R-96) |
| `HERALD_HTTP_ADDR` | herald-api | `:8080` | The address the Tenant API serves REST on (R-96) |
| `HERALD_ALLOW_INSECURE_ENDPOINTS` | herald-api, herald-dispatcher | `false` | Relaxes the Endpoint rules for local development ([security.md](security.md): R-50, R-68, R-69; R-99) |
| `HERALD_RETRY_SCALE` | herald-dispatcher | `1` | Shrinks the retry schedule for local development (R-76, R-77, R-78) |
| `HERALD_OTLP_URL` | herald-api, herald-dispatcher | not set: no spans exported | Where spans go ([observability.md](observability.md): R-54, R-115) |
| `HERALD_BUILD_VERSION` | herald-api, herald-dispatcher | `dev` | Identifies the build on every span (R-55) |
| `HERALD_RUN_ID` | herald-api, herald-dispatcher | empty | Identifies the run on every span (R-55) |
| `HERALD_PPROF_ADDR` | herald-api, herald-dispatcher | not set: no profiles served | The internal address profiles are served on (R-57) |

### R-95 · planned

When a setting a service requires isn't set, the service MUST NOT start, and MUST exit with a non-zero status and an error, on standard error, that names the setting. `HERALD_DATABASE_URL`, `HERALD_KEYRING` and `HERALD_NATS_URL` are required to run herald-api or herald-dispatcher, and only `HERALD_DATABASE_URL` to run their `migrate` command (R-100). A setting set to a value its own requirement doesn't allow stops the service in the same way: R-54, R-69, R-77 and R-97.

Why: a service started without its database, keys or NATS can't do its work, and naming the missing setting is the quickest way to the fix. The `migrate` command touches only the database, so a deploy can run it before the services' other settings exist.

| Command | Not set | Result |
|---|---|---|
| `herald-api` | `HERALD_KEYRING` | exits, naming `HERALD_KEYRING` |
| `herald-dispatcher` | `HERALD_NATS_URL` | exits, naming `HERALD_NATS_URL` |
| `herald-api migrate` | `HERALD_KEYRING`, `HERALD_NATS_URL` | migrates |
| `herald-dispatcher migrate` | `HERALD_DATABASE_URL` | exits, naming `HERALD_DATABASE_URL` |

### R-96 · planned

herald-api MUST serve the Tenant API over gRPC on `HERALD_GRPC_ADDR` and over REST on `HERALD_HTTP_ADDR`, and on `:9090` and `:8080` when they aren't set.

Why: a deployment that doesn't set them, such as the local stack, relies on the defaults, so changing one moves the Tenant API under every such deployment. They are the ports gRPC and HTTP services commonly use (#114).

### R-97 · planned

`HERALD_KEYRING` MUST be a comma-separated list of keys, each a key ID, a colon, and a 32-byte key in padded standard base64: `<id>:<key>[,<id>:<key>...]`. Each key ID MUST be 1 to `keyring-key-id-bytes` bytes, each one of `A-Z`, `a-z`, `0-9`, `_` and `-`, and MUST differ from the others. A service given any other value MUST NOT start.

Why: the keys are AES-256 keys ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)), and each encrypted secret records the ID of the key that encrypted it. Whitespace isn't trimmed, so a space after a comma is a mistake that stops the service, rather than a key ID that silently differs.

| `HERALD_KEYRING` | Service |
|---|---|
| `2026-11:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=` | starts |
| `2026-11:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=,2026-05:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=` | starts |
| `2026-11:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=, 2026-05:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=` | doesn't start |
| `2026-11:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=,2026-11:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=` | doesn't start |
| `2026-11:aW9FajNMMmtPb1NMRDVDZ0hoT3g=` | doesn't start |
| `2026.11:ioEj3L2kOoSLD5CgHhOxHIJpq/rigH1TSXUCJaDZFRs=` | doesn't start |

### R-98 · planned

herald-api MUST encrypt every new signing secret with the first key in `HERALD_KEYRING`, and both services MUST decrypt a secret with whichever listed key encrypted it.

Why: rotating the key without stopping Herald (R-46) then takes two deploys: first add the new key after the current one in every service, so each can decrypt with it; then move it to the front. Moving it to the front first would have herald-api encrypt with a key a dispatcher doesn't know yet.

### R-99 · planned

When `HERALD_ALLOW_INSECURE_ENDPOINTS` is true, as R-69 reads it, each service MUST log a warning, as it starts, that insecure Endpoints are allowed for local development only.

Why: the setting lets Herald deliver over plain http to private addresses, including the network it runs in ([security.md](security.md)). As for R-78, the warning keeps it from reaching production unnoticed.

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

## Deploying, starting and stopping

### R-100 · planned

herald-api and herald-dispatcher MUST each have a `migrate` command that applies its own schema's migrations and exits, with status 0 once the schema is current. Started without a command, a service MUST NOT migrate, and assumes its schema is current. Given any other command, or an argument after `migrate`, it MUST exit with a non-zero status without doing anything.

Why: a deploy runs each service's migrations as a step of their own, before any replica of the new build starts, so a migration that fails stops the deploy instead of crash-looping every replica, and replicas that start together don't each try to migrate ([ADR 0010](../docs/adr/0010-migrations-as-a-one-shot-step.md)). Each service owns its schema ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)).

### R-102 · planned

When two `migrate` runs for the same schema overlap, as a retried job or two overlapping deploys start them, they MUST take turns, and both MUST succeed.

Why: a deployer can't always prevent the overlap, and two runs applying the same migration at once would fail or apply it twice ([ADR 0010](../docs/adr/0010-migrations-as-a-one-shot-step.md)).

| Runs | Result |
|---|---|
| two `herald-api migrate` at once, on a schema two migrations behind | one applies both migrations, the other then finds the schema current; both exit 0 |
| `herald-api migrate` and `herald-dispatcher migrate` at once | each applies its own schema's migrations; both exit 0 |

### R-103 · planned

A service MUST start, and keep running, while Postgres or NATS is unreachable, and keep trying to reach it.

Why: in production, Postgres and NATS are shared infrastructure that is normally up before Herald and stays up across its deploys, so an outage of either is one to ride out, not a reason to stop. herald-api accepts Messages without NATS, and they wait in the Outbox ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)); a dispatcher still makes Attempts for the Deliveries it stored. Exiting would only add restarts (decided in #114).

| Unreachable at start | herald-api | herald-dispatcher |
|---|---|---|
| NATS | starts; accepts Messages, which wait in the Outbox; logs an error | starts; makes Attempts for stored Deliveries; logs an error |
| Postgres | starts; logs an error | starts; logs an error |
| neither | starts | starts |

### R-117 · planned

While Postgres or NATS is unreachable, a service MUST log an error that names it: as it starts, if it is unreachable then, and at least once every `dependency-log-interval` until it is reachable again.

Why: a service that rides out an outage (R-103) must not hide it; the error lines keep it visible to an operator watching the logs (decided in #114).

### R-104 · planned

When a service receives `SIGTERM` or `SIGINT`, it MUST stop taking new work, let the work in progress finish, and exit within `max-shutdown`: with status 0 when all of it finished, and otherwise, having cut off what hadn't, with a non-zero status.

Why: Docker and Kubernetes stop a container with `SIGTERM` and kill it 10 s later, so a service still finishing then is killed mid-shutdown. herald-api lets calls in progress finish, and leaves the Messages its relay hadn't handed off in the Outbox for the next relay; herald-dispatcher stores and replies to the Batches it received, and lets its Attempts in flight finish (R-101).

### R-101 · planned

When herald-dispatcher is told to stop, it MUST let its Attempts in flight finish for up to `attempt-drain-timeout`, and then cut off those still running: a cut-off Attempt isn't counted, and its Delivery is due again at once.

Why: a cut-off Attempt may or may not have reached the Endpoint, so it is made again, which at-least-once delivery allows (R-17), rather than counted as a failure the Endpoint didn't cause. Giving the Delivery back at once lets another dispatcher make the Attempt without waiting `lease-duration` (R-91).

### R-105 · planned

When a service receives a second `SIGTERM` or `SIGINT` while it is shutting down, it MUST exit at once.

Why: an operator whose service hangs on shutdown can stop it without reaching for `SIGKILL`.

## herald-admin

herald-admin creates Tenants and their API keys. It writes Postgres directly, needing only `HERALD_DATABASE_URL`, and touches only herald-api's schema ([ADR 0009](../docs/adr/0009-herald-admin-writes-ingest-directly.md)). Provisioning scripts run it, so what it prints and how it exits are part of what operators use.

### R-106 · planned

`herald-admin tenant create --name <name>` MUST create a Tenant with that name and print its ID, and nothing else, on one line of standard output. A name that is empty or only whitespace MUST be refused as a usage error (R-109).

Why: a provisioning script reads the ID from standard output, to create the Tenant's API key with it.

### R-107 · planned

`herald-admin apikey create --tenant <id>` MUST create an API key for that Tenant and print the key, and nothing else, on one line of standard output, and print on standard error that the key is shown only once.

Why: Herald stores only the key's hash (R-22), so this is the only time anyone sees the key. Alone on standard output, it can be piped into a secret store, and the note goes to standard error so it doesn't end up there.

### R-108 · planned

`herald-admin help`, `-h`, `-help` and `--help` MUST print the usage, listing every command, on standard output and exit 0. herald-admin with no arguments MUST print the same usage on standard error and exit 2.

Why: asking for help succeeds; running herald-admin with nothing to do is a usage error (R-109).

### R-109 · planned

herald-admin MUST exit 0 when its command succeeds, 2 when it is used wrongly, and 1 when a command used rightly fails. Every error MUST go to standard error, starting `herald-admin:`.

Why: a script can then tell a mistake in its own call, which retrying won't fix, from a failure such as an unreachable database, which it may retry. Nothing but a command's result goes to standard output (R-106, R-107).

| Call | Exit status |
|---|---|
| `tenant create --name Acme` | 0 |
| `tenant create` | 2 |
| `tenant create --name " "` | 2 |
| `tenant create --name Acme extra` | 2 |
| `tenant delete` | 2 |
| `apikey create --tenant not-a-uuid` | 2 |
| `apikey create --tenant 0198a1b2-0000-7000-8000-0000000000aa`, for a Tenant that doesn't exist | 1 |
| `tenant create --name Acme`, with `HERALD_DATABASE_URL` not set | 1 |
| `tenant create --name Acme`, with Postgres unreachable | 1 |
