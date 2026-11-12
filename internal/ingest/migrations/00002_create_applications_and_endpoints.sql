-- +goose Up
CREATE TABLE ingest.applications (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    tenant_id  uuid NOT NULL REFERENCES ingest.tenants (id),
    -- Not blank (at least one character that isn't whitespace), and at most
    -- 256 bytes.
    name       text NOT NULL CHECK (name ~ '\S' AND octet_length(name) <= 256),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A Tenant's Applications, in the order they are listed.
CREATE INDEX ON ingest.applications (tenant_id, id);

CREATE TABLE ingest.endpoints (
    -- No default: herald-api generates the ID, because the Endpoint's signing
    -- secrets are encrypted with it as associated data before the row exists.
    id             uuid PRIMARY KEY,
    application_id uuid NOT NULL REFERENCES ingest.applications (id),
    url            text NOT NULL,
    -- Event type patterns (internal/eventtype). Empty accepts every Event type.
    filter         text[] NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- An Application's Endpoints, in the order they are listed.
CREATE INDEX ON ingest.endpoints (application_id, id);

-- An Endpoint's signing secrets. Each Attempt is signed with all of them
-- (ADR 0003); signing-secret rotation adds one and expires the old one.
CREATE TABLE ingest.signing_secrets (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    endpoint_id uuid NOT NULL REFERENCES ingest.endpoints (id),
    -- The signing secret encrypted with the keyring (internal/keyring), with
    -- the Endpoint's ID in canonical text form as associated data. The secret
    -- itself is never stored.
    ciphertext  bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ON ingest.signing_secrets (endpoint_id);

-- +goose Down
DROP TABLE ingest.signing_secrets;
DROP TABLE ingest.endpoints;
DROP TABLE ingest.applications;
