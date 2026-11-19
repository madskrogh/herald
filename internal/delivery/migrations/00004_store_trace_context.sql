-- +goose Up
-- The Application each Message was sent to, which Attempt spans carry. NULL
-- for a Message handed off before Batches carried it.
ALTER TABLE delivery.messages ADD COLUMN application_id uuid;

-- The context of the span that stored each Delivery, as a W3C traceparent.
-- Attempts run seconds to a day later, on any replica, where the Batch's
-- context is lost; each continues the Message's trace from this. NULL for a
-- Delivery stored before this column, or without a trace; each of its
-- Attempts starts a trace of its own.
ALTER TABLE delivery.deliveries ADD COLUMN traceparent text;

-- +goose Down
ALTER TABLE delivery.deliveries DROP COLUMN traceparent;
ALTER TABLE delivery.messages DROP COLUMN application_id;
