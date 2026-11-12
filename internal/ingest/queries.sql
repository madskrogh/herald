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
