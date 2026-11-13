-- +goose Up
CREATE TABLE ingest.messages (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    tenant_id       uuid NOT NULL REFERENCES ingest.tenants (id),
    application_id  uuid NOT NULL REFERENCES ingest.applications (id),
    event_type      text NOT NULL,
    -- The JSON payload exactly as the Tenant submitted it. Deliveries sign it
    -- byte for byte, so it is stored as bytes rather than parsed.
    payload         bytea NOT NULL,
    -- The Tenant's Idempotency-Key, or NULL for none. A key reserves a Message
    -- for 24 hours from created_at. It isn't deleted when it expires: the next
    -- Message that reuses it clears it from the old Message first.
    idempotency_key text,
    -- Set by herald-api from its own clock, not by Postgres: whether an
    -- Idempotency-Key has expired is decided against the same clock.
    created_at      timestamptz NOT NULL,
    -- The outbox marker (ADR 0002): NULL until the relay has handed off the
    -- Message's Deliveries. A Message without Deliveries has nothing to hand
    -- off, and is written with it already set.
    handed_off_at   timestamptz
);

-- One Message per Tenant and Idempotency-Key. Concurrent requests with the
-- same key wait on this index, so only one of them creates a Message.
CREATE UNIQUE INDEX ON ingest.messages (tenant_id, idempotency_key);

-- The outbox: the Messages the relay has yet to hand off, oldest first. Only
-- they are in the index, so it stays as small as the backlog.
CREATE INDEX messages_outbox ON ingest.messages (id) WHERE handed_off_at IS NULL;

CREATE TABLE ingest.deliveries (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    message_id  uuid NOT NULL REFERENCES ingest.messages (id),
    endpoint_id uuid NOT NULL REFERENCES ingest.endpoints (id),
    UNIQUE (message_id, endpoint_id)
);

-- +goose Down
DROP TABLE ingest.deliveries;
DROP TABLE ingest.messages;
