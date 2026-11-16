-- +goose Up
-- A worker leases the Deliveries it claims, for one Attempt each. A lease that
-- runs out, because its worker died or hung, makes the Delivery due again.
ALTER TABLE delivery.deliveries
    -- Identifies the current lease. Recording an Attempt requires it, so a
    -- worker whose lease ran out and was claimed again can't record over the
    -- new holder.
    ADD COLUMN lease_id     uuid,
    -- When the current lease runs out.
    ADD COLUMN leased_until timestamptz,
    -- How many Attempts have been recorded, which decides when the next is
    -- due and when the retry schedule has run out.
    ADD COLUMN attempt_count integer NOT NULL DEFAULT 0;

-- Every Attempt made for a Delivery, kept whether it succeeded or not.
CREATE TABLE delivery.attempts (
    delivery_id   uuid NOT NULL REFERENCES delivery.deliveries (id),
    -- Counts the Delivery's Attempts from 1.
    number        integer NOT NULL CHECK (number > 0),
    started_at    timestamptz NOT NULL,
    -- NULL when there was no response: a timeout or connection error.
    status_code   integer,
    -- Up to the response headers or the error.
    latency_ms    integer NOT NULL CHECK (latency_ms >= 0),
    -- The start of the response body, bounded by the dispatcher.
    response_body bytea NOT NULL,
    -- Why there was no response, or NULL if there was one.
    error         text,
    PRIMARY KEY (delivery_id, number)
);

-- Workers find pending Deliveries by when they are due, and each Endpoint's
-- by Endpoint.
CREATE INDEX deliveries_due ON delivery.deliveries (next_attempt_at)
    WHERE state = 'pending';
CREATE INDEX deliveries_endpoint_due ON delivery.deliveries (endpoint_id, next_attempt_at)
    WHERE state = 'pending';
-- Counting each Endpoint's Attempts in flight reads only leased Deliveries,
-- a few per Endpoint however large its backlog.
CREATE INDEX deliveries_leased ON delivery.deliveries (endpoint_id, leased_until)
    WHERE state = 'pending' AND leased_until IS NOT NULL;

-- +goose Down
DROP INDEX delivery.deliveries_leased;
DROP INDEX delivery.deliveries_endpoint_due;
DROP INDEX delivery.deliveries_due;
DROP TABLE delivery.attempts;
ALTER TABLE delivery.deliveries
    DROP COLUMN attempt_count,
    DROP COLUMN leased_until,
    DROP COLUMN lease_id;
