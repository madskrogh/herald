-- +goose Up
-- A worker leases the Deliveries it claims, for one Attempt each. A lease that
-- runs out, because its worker died or hung, makes the Delivery due again.
ALTER TABLE delivery.deliveries
    -- Identifies the current lease. Recording an Attempt requires it, so a
    -- worker whose lease ran out and was claimed again can't record over the
    -- new holder.
    ADD COLUMN lease_id     uuid,
    -- When the current lease runs out.
    ADD COLUMN leased_until timestamptz;

-- Workers find pending Deliveries by when they are due, and each Endpoint's
-- by Endpoint.
CREATE INDEX deliveries_due ON delivery.deliveries (next_attempt_at)
    WHERE state = 'pending';
CREATE INDEX deliveries_endpoint_due ON delivery.deliveries (endpoint_id, next_attempt_at)
    WHERE state = 'pending';

-- +goose Down
DROP INDEX delivery.deliveries_endpoint_due;
DROP INDEX delivery.deliveries_due;
ALTER TABLE delivery.deliveries
    DROP COLUMN leased_until,
    DROP COLUMN lease_id;
