-- +goose Up
CREATE TABLE ingest.applications (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    tenant_id  uuid NOT NULL REFERENCES ingest.tenants (id),
    -- Not blank: at least one character that isn't whitespace.
    name       text NOT NULL CHECK (name ~ '\S' AND octet_length(name) <= 256),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A Tenant's Applications, in the order they are listed.
CREATE INDEX ON ingest.applications (tenant_id, id);

-- +goose Down
DROP TABLE ingest.applications;
