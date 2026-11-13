-- +goose Up
-- Migrate creates the schema before goose runs, since goose keeps its version
-- table in it. Creating it here too keeps the migrations a complete
-- description of the schema, which is what sqlc reads.
CREATE SCHEMA IF NOT EXISTS delivery;

-- Messages handed off by herald-api. IDs come from herald-api, so a Batch
-- sent again finds its rows already there (ADR 0002).
CREATE TABLE delivery.messages (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL,
    event_type text NOT NULL,
    -- The JSON payload exactly as the Tenant submitted it. Attempts sign it
    -- byte for byte, so it is kept as bytes rather than as json or jsonb.
    -- It is stored once per Message, however many Deliveries it has.
    payload    bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE delivery.deliveries (
    id              uuid PRIMARY KEY,
    message_id      uuid NOT NULL REFERENCES delivery.messages (id),
    -- A copy of the Endpoint's details at the time the Message was accepted,
    -- so the dispatcher never reads the ingest schema.
    endpoint_id     uuid NOT NULL,
    endpoint_url    text NOT NULL,
    -- The Endpoint's signing secrets, encrypted with the keyring
    -- (internal/keyring) with the Endpoint's ID in canonical text form as
    -- associated data. They are decrypted only just before signing. Every
    -- Attempt is signed, so there is at least one.
    signing_secrets bytea[] NOT NULL CHECK (cardinality(signing_secrets) > 0),
    state           text NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'succeeded', 'failed')),
    -- When the next Attempt is due. Workers pick up pending Deliveries whose
    -- time has come.
    next_attempt_at timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE delivery.deliveries;
DROP TABLE delivery.messages;
