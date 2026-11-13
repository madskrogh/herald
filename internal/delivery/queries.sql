-- name: InsertMessage :batchexec
-- A Message sent again, after a lost reply, is already stored and left as it
-- is.
INSERT INTO delivery.messages (id, tenant_id, event_type, payload)
VALUES (@id, @tenant_id, @event_type, @payload)
ON CONFLICT (id) DO NOTHING;

-- name: InsertDelivery :batchexec
-- A new Delivery is pending and due at @now. One sent again, after a lost
-- reply, is already stored and left as it is.
INSERT INTO delivery.deliveries (id, message_id, endpoint_id, endpoint_url, signing_secrets, state, next_attempt_at)
VALUES (@id, @message_id, @endpoint_id, @endpoint_url, @signing_secrets::bytea[], 'pending', @now)
ON CONFLICT (id) DO NOTHING;

-- name: GetDelivery :one
SELECT d.id, d.message_id, m.tenant_id, m.event_type, m.payload,
       d.endpoint_id, d.endpoint_url, d.signing_secrets, d.state, d.next_attempt_at
FROM delivery.deliveries d
JOIN delivery.messages m ON m.id = d.message_id
WHERE d.id = @id;
