# What Herald stores

The Postgres columns the scenario writes, to set up a run, and reads, for where the run left its Messages and Deliveries. They are listed, with their types, in the [Postgres column list](postgres-columns.md). Nothing else Herald stores is in the spec: not its other columns, tables or indexes. Behaviour that rests on an index, such as the Idempotency-Key, is stated and checked through the Tenant API ([ADR 0007](../docs/adr/0007-code-regenerated-from-the-spec.md)).

### R-84 · implemented

Once its migrations have run, Herald's Postgres MUST have each column the [column list](postgres-columns.md) names, in its table, of its type, and allowing NULL only where the list allows it. Each column MUST hold what the list says it holds.

Why: the scenario reads where a run left its Messages and Deliveries through these columns, and sets up the run through them, so it holds any build to the same data without reading that build's code ([ADR 0007](../docs/adr/0007-code-regenerated-from-the-spec.md)). A column the list doesn't name may change freely.

### R-85 · implemented

When a row is written to a table with only the columns the [column list](postgres-columns.md) says the scenario sets, Postgres MUST accept it, and MUST generate each of the table's columns the list says the database sets.

Why: the scenario creates its Tenant and API key as herald-admin does ([ADR 0009](../docs/adr/0009-herald-admin-writes-ingest-directly.md)), knowing only the columns the list names, so every other column of those tables has to fill itself in: with a default, or with NULL.
