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

-- name: ClaimDueDeliveries :many
-- Leases up to @max_deliveries pending Deliveries due at @now, until
-- @leased_until. A leased Delivery is due again when its lease runs out, so
-- one whose worker never records its Attempt is claimed again.
WITH claimed AS (
    UPDATE delivery.deliveries d
    SET lease_id = gen_random_uuid(),
        leased_until = @leased_until::timestamptz,
        next_attempt_at = @leased_until::timestamptz
    WHERE d.id IN (
        SELECT due.id FROM delivery.deliveries due
        WHERE due.state = 'pending' AND due.next_attempt_at <= @now::timestamptz
        ORDER BY due.next_attempt_at
        LIMIT @max_deliveries::integer
        FOR UPDATE SKIP LOCKED
    )
    RETURNING d.id, d.message_id, d.endpoint_id, d.endpoint_url, d.signing_secrets, d.state, d.next_attempt_at, d.lease_id
)
SELECT c.id, c.message_id, m.tenant_id, m.event_type, m.payload,
       c.endpoint_id, c.endpoint_url, c.signing_secrets, c.state, c.next_attempt_at,
       c.lease_id::uuid AS lease_id
FROM claimed c
JOIN delivery.messages m ON m.id = c.message_id;
