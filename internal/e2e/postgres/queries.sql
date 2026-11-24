-- The harness's queries. They read and write only the columns in the Postgres
-- column list (spec/postgres-columns.md), through schema.sql.

-- name: CreateTenant :one
INSERT INTO ingest.tenants (name)
VALUES (@name)
RETURNING id;

-- name: CreateAPIKey :exec
INSERT INTO ingest.api_keys (tenant_id, key_hash)
VALUES (@tenant_id, @key_hash);

-- name: ListTenantMessages :many
-- Every Message of the Tenant, in ID order, with whether it is handed off
-- and whether it is Parked.
SELECT id,
       (handed_off_at IS NOT NULL)::boolean AS handed_off,
       (parked_at IS NOT NULL)::boolean AS parked
FROM ingest.messages
WHERE tenant_id = @tenant_id
ORDER BY id;

-- name: ListMessageDeliveries :many
SELECT id, message_id, endpoint_id, state
FROM delivery.deliveries
WHERE message_id = @message_id
ORDER BY id;

-- name: CountAttempts :one
SELECT count(*)::integer AS attempts
FROM delivery.attempts
WHERE delivery_id = @delivery_id;
