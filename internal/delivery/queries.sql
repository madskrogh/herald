-- name: InsertMessage :batchexec
-- A Message sent again, after a lost reply, is already stored and left as it
-- is.
INSERT INTO delivery.messages (id, tenant_id, application_id, event_type, payload)
VALUES (@id, @tenant_id, sqlc.narg(application_id), @event_type, @payload)
ON CONFLICT (id) DO NOTHING;

-- name: InsertDelivery :batchexec
-- A new Delivery is pending and due at @now. One sent again, after a lost
-- reply, is already stored and left as it is, with the trace context it was
-- first stored under.
INSERT INTO delivery.deliveries (id, message_id, endpoint_id, endpoint_url, signing_secrets, state, next_attempt_at, traceparent)
VALUES (@id, @message_id, @endpoint_id, @endpoint_url, @signing_secrets::bytea[], 'pending', @now, NULLIF(@traceparent::text, ''))
ON CONFLICT (id) DO NOTHING;

-- name: GetDelivery :one
SELECT d.id, d.message_id, m.tenant_id, m.application_id, m.event_type, m.payload,
       d.endpoint_id, d.endpoint_url, d.signing_secrets, d.state, d.next_attempt_at,
       coalesce(d.traceparent, '')::text AS traceparent
FROM delivery.deliveries d
JOIN delivery.messages m ON m.id = d.message_id
WHERE d.id = @id;

-- name: ListMessageDeliveries :many
SELECT d.id, d.message_id, m.tenant_id, m.application_id, m.event_type, m.payload,
       d.endpoint_id, d.endpoint_url, d.signing_secrets, d.state, d.next_attempt_at,
       coalesce(d.traceparent, '')::text AS traceparent
FROM delivery.deliveries d
JOIN delivery.messages m ON m.id = d.message_id
WHERE d.message_id = @message_id
ORDER BY d.id;

-- name: ListDueEndpoints :many
-- Lists up to @max_endpoints Endpoints with Deliveries due at @now, most
-- overdue first, leaving out those with @max_in_flight unexpired leases: an
-- Endpoint at the cap must not use up a claim's limit. The count here only
-- narrows the choice; ClaimEndpointDeliveries decides under the Endpoint's
-- lock. Ties go to the lower Endpoint ID, so the order is stable.
WITH at_cap AS (
    SELECT f.endpoint_id FROM delivery.deliveries f
    WHERE f.state = 'pending' AND f.leased_until > @now::timestamptz
    GROUP BY f.endpoint_id
    HAVING count(*) >= @max_in_flight::integer
)
SELECT due.endpoint_id FROM delivery.deliveries due
WHERE due.state = 'pending' AND due.next_attempt_at <= @now::timestamptz
  AND due.endpoint_id NOT IN (SELECT endpoint_id FROM at_cap)
GROUP BY due.endpoint_id
ORDER BY min(due.next_attempt_at), due.endpoint_id
LIMIT @max_endpoints::integer;

-- name: TryLockEndpoint :one
-- Takes the Endpoint's claim lock until the transaction ends, or returns
-- false at once if another transaction holds it. Counting an Endpoint's
-- leases and adding to them under this lock keeps the cap across dispatcher
-- replicas. The two-key form keeps these locks apart from the migrate
-- step's, which use one key.
SELECT pg_try_advisory_xact_lock(hashtext('herald.delivery.endpoint'), hashtext(sqlc.arg(endpoint_id)::uuid::text))::boolean AS locked;

-- name: CountInFlight :one
-- Counts the Endpoint's unexpired leases: its Attempts in flight. A lease
-- whose worker died stops counting when it runs out.
--
-- Kept apart from ClaimEndpointDeliveries: merging the count into the
-- claim's LIMIT is valid Postgres, but sqlc v1.30 rejects every form of it.
SELECT count(*)::integer FROM delivery.deliveries
WHERE endpoint_id = @endpoint_id AND state = 'pending' AND leased_until > @now::timestamptz;

-- name: ClaimEndpointDeliveries :many
-- Leases up to @max_deliveries of the Endpoint's pending Deliveries due at
-- @now, until @leased_until. A leased Delivery is due again when its lease
-- runs out, so one whose worker never records its Attempt is claimed again.
WITH claimed AS (
    UPDATE delivery.deliveries d
    SET lease_id = gen_random_uuid(),
        leased_until = @leased_until::timestamptz,
        next_attempt_at = @leased_until::timestamptz
    WHERE d.id IN (
        SELECT due.id FROM delivery.deliveries due
        WHERE due.endpoint_id = @endpoint_id::uuid
          AND due.state = 'pending' AND due.next_attempt_at <= @now::timestamptz
        ORDER BY due.next_attempt_at
        LIMIT @max_deliveries::integer
        FOR UPDATE SKIP LOCKED
    )
    RETURNING d.id, d.message_id, d.endpoint_id, d.endpoint_url, d.signing_secrets, d.state, d.next_attempt_at, d.lease_id, d.attempt_count, d.traceparent
)
SELECT c.id, c.message_id, m.tenant_id, m.application_id, m.event_type, m.payload,
       c.endpoint_id, c.endpoint_url, c.signing_secrets, c.state, c.next_attempt_at,
       c.lease_id::uuid AS lease_id, c.attempt_count, coalesce(c.traceparent, '')::text AS traceparent
FROM claimed c
JOIN delivery.messages m ON m.id = c.message_id;

-- name: FinishAttempt :one
-- Ends the lease @lease_id, counts its Attempt and moves the Delivery to
-- @state, due again at @next_attempt_at if that is given. Only the current
-- lease matches: a worker whose lease ran out and was claimed again gets no
-- row, so it can't record over the new holder. Returns the Attempt's number.
UPDATE delivery.deliveries
SET attempt_count = attempt_count + 1,
    state = @state,
    next_attempt_at = coalesce(sqlc.narg(next_attempt_at)::timestamptz, next_attempt_at),
    lease_id = NULL,
    leased_until = NULL
WHERE id = @id AND lease_id = @lease_id::uuid
RETURNING attempt_count;

-- name: ReleaseLease :execrows
-- Ends the lease @lease_id without an Attempt and makes the Delivery due at
-- @now, as when shutdown cuts its Attempt off. Only the current lease
-- matches.
UPDATE delivery.deliveries
SET next_attempt_at = @now::timestamptz,
    lease_id = NULL,
    leased_until = NULL
WHERE id = @id AND lease_id = @lease_id::uuid;

-- name: InsertAttempt :exec
INSERT INTO delivery.attempts (delivery_id, number, started_at, status_code, latency_ms, response_body, error)
VALUES (@delivery_id, @number, @started_at, sqlc.narg(status_code), @latency_ms, @response_body, sqlc.narg(error));

-- name: ListAttempts :many
SELECT number, started_at, status_code, latency_ms, response_body, error
FROM delivery.attempts
WHERE delivery_id = @delivery_id
ORDER BY number;
