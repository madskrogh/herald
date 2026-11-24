-- +goose Up
-- The columns of Herald's Postgres that the harness relies on, and no others:
-- the Postgres column list (spec/postgres-columns.md). sqlc compiles the
-- harness's queries against this schema rather than Herald's migrations, so
-- a query that reads a column the list doesn't name fails to generate.
-- pgcolumns' tests apply it as a migration, never run against the stack, and
-- check it against the list.

CREATE SCHEMA ingest;

CREATE TABLE ingest.tenants (
    -- The list says only that the database sets it; any default says so.
    id   uuid NOT NULL DEFAULT uuidv7(),
    name text NOT NULL
);

CREATE TABLE ingest.api_keys (
    tenant_id uuid  NOT NULL,
    key_hash  bytea NOT NULL
);

CREATE TABLE ingest.messages (
    id              uuid NOT NULL,
    tenant_id     uuid NOT NULL,
    handed_off_at timestamptz,
    parked_at     timestamptz
);

CREATE SCHEMA delivery;

CREATE TABLE delivery.deliveries (
    id          uuid NOT NULL,
    message_id  uuid NOT NULL,
    endpoint_id uuid NOT NULL,
    state       text NOT NULL
);

CREATE TABLE delivery.attempts (
    delivery_id uuid NOT NULL
);
