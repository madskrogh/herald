# Migrations run as a one-shot step before the services start

Each service owns the migrations for its own schema ([ADR 0002](0002-two-services-outbox-nats-handoff.md)): herald-api for `ingest`, herald-dispatcher for `delivery`. They are embedded in the service's binary and applied by a separate `migrate` step, which runs to completion before the service starts. A service never migrates at startup.

- **A bad migration stops the deploy.** If a migration fails, the step fails and no replica of the new build starts, instead of every new replica crash-looping on it.
- **Startup stays fast and uniform.** A long migration doesn't hold up a replica's readiness or get it killed by a health probe, and replicas that start together don't each try to migrate.

## Considered Options

- **Each service migrates at startup**: rejected, for the two reasons above.

## Consequences

- A deploy has an ordered step: the service waits for its `migrate` step to succeed. Locally that is a dependency in the compose file; on Kubernetes (M5) it is a Job.
- A service assumes its schema is already current.
- During a rollout, replicas of the previous build run against the new schema, so a migration must keep the previous build working.
- A deployer can start the step twice at once, as a retried Job or overlapping deploys do, so overlapping runs must be safe. That is part of what operators use, and is stated in the spec ([ADR 0007](0007-code-regenerated-from-the-spec.md)).
