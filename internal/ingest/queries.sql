-- name: CreateTenant :one
INSERT INTO ingest.tenants (name)
VALUES (@name)
RETURNING *;

-- name: CreateAPIKey :exec
INSERT INTO ingest.api_keys (tenant_id, key_hash)
VALUES (@tenant_id, @key_hash);

-- name: GetTenantByAPIKeyHash :one
SELECT t.id, t.name, t.created_at
FROM ingest.api_keys k
JOIN ingest.tenants t ON t.id = k.tenant_id
WHERE k.key_hash = @key_hash;

-- name: CreateApplication :one
INSERT INTO ingest.applications (tenant_id, name)
VALUES (@tenant_id, @name)
RETURNING *;

-- name: GetApplication :one
SELECT *
FROM ingest.applications
WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListApplications :many
-- A page of the Tenant's Applications: those after the ID @after, in ID order.
SELECT *
FROM ingest.applications
WHERE tenant_id = @tenant_id AND id > @after
ORDER BY id
LIMIT @max_results;

-- name: LockApplicationForEndpoints :one
-- Locks the Tenant's Application while an Endpoint is added to it, so that
-- concurrent additions count its Endpoints one at a time. FOR NO KEY UPDATE
-- rather than FOR UPDATE: a Message insert takes FOR KEY SHARE on the
-- Application through its foreign key, which FOR UPDATE would block.
SELECT id
FROM ingest.applications
WHERE tenant_id = @tenant_id AND id = @id
FOR NO KEY UPDATE;

-- name: CountEndpoints :one
SELECT count(*)
FROM ingest.endpoints
WHERE application_id = @application_id;

-- name: CreateEndpoint :one
-- Creates the Endpoint only if its Application belongs to the Tenant, so that
-- no row comes back for another Tenant's Application.
INSERT INTO ingest.endpoints (id, application_id, url, filter)
SELECT @id, a.id, @url, @filter::text[]
FROM ingest.applications a
WHERE a.tenant_id = @tenant_id AND a.id = @application_id
RETURNING *;

-- name: CreateSigningSecret :exec
INSERT INTO ingest.signing_secrets (endpoint_id, ciphertext)
VALUES (@endpoint_id, @ciphertext);

-- name: GetEndpoint :one
SELECT e.*
FROM ingest.endpoints e
JOIN ingest.applications a ON a.id = e.application_id
WHERE a.tenant_id = @tenant_id AND e.application_id = @application_id AND e.id = @id;

-- name: ListEndpoints :many
-- A page of the Endpoints under the Tenant's Application: those after the ID
-- @after, in ID order.
SELECT e.*
FROM ingest.endpoints e
JOIN ingest.applications a ON a.id = e.application_id
WHERE a.tenant_id = @tenant_id AND e.application_id = @application_id AND e.id > @after
ORDER BY e.id
LIMIT @max_results;

-- name: ListEndpointFilters :many
-- The filters of every Endpoint under the Tenant's Application, for working
-- out a Message's Deliveries.
SELECT e.id, e.filter
FROM ingest.endpoints e
JOIN ingest.applications a ON a.id = e.application_id
WHERE a.tenant_id = @tenant_id AND e.application_id = @application_id
ORDER BY e.id;

-- name: CreateMessage :one
-- Creates the Message only if its Application belongs to the Tenant and no
-- Message of the Tenant's holds its Idempotency-Key (an empty key is none).
-- Otherwise no row comes back. A Message without Deliveries has nothing to
-- hand off, so it is written as handed off already and never enters the
-- outbox.
INSERT INTO ingest.messages (tenant_id, application_id, event_type, payload, idempotency_key, created_at, handed_off_at)
SELECT
    a.tenant_id, a.id, @event_type, @payload, NULLIF(@idempotency_key::text, ''), @now::timestamptz,
    CASE WHEN @has_deliveries::boolean THEN NULL ELSE @now::timestamptz END
FROM ingest.applications a
WHERE a.tenant_id = @tenant_id AND a.id = @application_id
ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: ReleaseIdempotencyKey :exec
-- Takes the Idempotency-Key from the Tenant's Message that holds it, if that
-- Message was created at or before @expired_by, so the key can be reused.
UPDATE ingest.messages
SET idempotency_key = NULL
WHERE tenant_id = @tenant_id AND idempotency_key = @idempotency_key::text AND created_at <= @expired_by;

-- name: GetMessageByIdempotencyKey :one
SELECT *
FROM ingest.messages
WHERE tenant_id = @tenant_id AND idempotency_key = @idempotency_key::text;

-- name: ListDeliveries :many
-- The Message's Deliveries, in Endpoint ID order.
SELECT id, endpoint_id
FROM ingest.deliveries
WHERE message_id = @message_id
ORDER BY endpoint_id;

-- name: CreateDeliveries :many
-- A Delivery of the Message to each of the Endpoints, in Endpoint ID order.
WITH created AS (
    INSERT INTO ingest.deliveries (message_id, endpoint_id)
    SELECT @message_id, unnest(@endpoint_ids::uuid[])
    RETURNING id, endpoint_id
)
SELECT id, endpoint_id
FROM created
ORDER BY endpoint_id;
