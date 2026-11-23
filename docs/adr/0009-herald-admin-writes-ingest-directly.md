# herald-admin writes the ingest schema directly

Tenants and their API keys are created by herald-admin, an operator CLI with a connection to Postgres, which writes the `ingest` schema directly. There is no admin API.

It is the smallest thing that works. Creating a Tenant is rare, and it is done by whoever runs Herald, who already has access to its database. An admin API would add a surface that no one uses yet. Tenant self-service is a non-goal.

## Considered Options

- **An admin API on herald-api**: deferred, until something needs to create Tenants other than whoever runs Herald.
- **Tenant self-service** (sign-up, dashboards): rejected as a non-goal. Tenants are SaaS products set up by whoever runs Herald.

## Consequences

- Kind, in [ADR 0007](0007-code-regenerated-from-the-spec.md)'s terms: architecture.
- An operator needs a database connection that can write the `ingest` schema. herald-admin works only where Postgres is reachable.
- herald-admin is on herald-api's side of [ADR 0002](0002-two-services-outbox-nats-handoff.md): it writes the schema herald-api owns, changes with it, and never touches `delivery`.
- Provisioning automation scripts the CLI.
- herald-admin's commands and what they print are part of what operators use, so they belong in the spec.
