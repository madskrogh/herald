-- +goose NO TRANSACTION
-- CONCURRENTLY builds the index without blocking writes to deliveries, which
-- would stop intake and every Attempt for as long as the build takes. It
-- can't run in a transaction, so goose runs each statement on its own.

-- +goose Up
-- Finds a Message's Deliveries. Deleting a Message checks its foreign key
-- through it too, rather than by scanning every Delivery.
--
-- A concurrent build that fails leaves an invalid index behind, which the
-- next run drops before building it again.
DROP INDEX CONCURRENTLY IF EXISTS delivery.deliveries_message;
CREATE INDEX CONCURRENTLY deliveries_message ON delivery.deliveries (message_id);

-- +goose Down
DROP INDEX CONCURRENTLY delivery.deliveries_message;
