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
