-- +goose Up
-- The trace context each Message was accepted under, as a W3C traceparent.
-- The relay claims a Message later, where the request's context is lost, and
-- continues the Message's trace from it. NULL for a Message stored before
-- this column, or without a trace; its handoff starts a trace of its own.
ALTER TABLE ingest.messages ADD COLUMN traceparent text;

-- +goose Down
ALTER TABLE ingest.messages DROP COLUMN traceparent;
