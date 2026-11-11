-- +goose Up
-- Migrate creates the schema before goose runs, since goose keeps its version
-- table in it. Creating it here too keeps the migrations a complete
-- description of the schema, which is what sqlc reads.
CREATE SCHEMA IF NOT EXISTS ingest;

CREATE TABLE ingest.tenants (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL CHECK (name <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ingest.api_keys (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    tenant_id  uuid NOT NULL REFERENCES ingest.tenants (id),
    -- SHA-256 of the key. The key itself is never stored.
    key_hash   bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE ingest.api_keys;
DROP TABLE ingest.tenants;
