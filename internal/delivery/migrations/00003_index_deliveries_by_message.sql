-- +goose Up
-- Finds a Message's Deliveries. Deleting a Message checks its foreign key
-- through it too, rather than by scanning every Delivery.
CREATE INDEX deliveries_message ON delivery.deliveries (message_id);

-- +goose Down
DROP INDEX delivery.deliveries_message;
