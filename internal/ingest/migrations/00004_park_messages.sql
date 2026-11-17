-- +goose Up
ALTER TABLE ingest.messages
    -- Set when the relay parks the Message: a dispatcher rejects it every time,
    -- so it is left out of the outbox rather than blocking the Messages
    -- behind it. Clearing both columns returns it to the outbox.
    ADD COLUMN parked_at  timestamptz,
    -- The error the dispatcher gave when it rejected the Message alone.
    ADD COLUMN park_error text,
    ADD CONSTRAINT messages_parked CHECK ((parked_at IS NULL) = (park_error IS NULL));

-- A parked Message is not in the outbox, so the index leaves it out too.
DROP INDEX ingest.messages_outbox;
CREATE INDEX messages_outbox ON ingest.messages (id) WHERE handed_off_at IS NULL AND parked_at IS NULL;

-- +goose Down
DROP INDEX ingest.messages_outbox;
CREATE INDEX messages_outbox ON ingest.messages (id) WHERE handed_off_at IS NULL;

ALTER TABLE ingest.messages
    DROP CONSTRAINT messages_parked,
    DROP COLUMN park_error,
    DROP COLUMN parked_at;
