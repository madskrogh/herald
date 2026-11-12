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
