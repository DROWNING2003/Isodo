-- name: LockTenantAPIKeys :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(tenant_id)::text, 1));

-- name: CountActiveAPIKeys :one
SELECT count(*)::bigint
FROM api_keys
WHERE tenant_id = sqlc.arg(tenant_id)::text::uuid AND revoked_at IS NULL;

-- name: CreateAPIKey :one
INSERT INTO api_keys (id, tenant_id, name, secret_hash, prefix, scopes, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(tenant_id)::text::uuid, sqlc.arg(name), sqlc.arg(secret_hash), sqlc.arg(prefix), sqlc.arg(scopes), sqlc.narg(expires_at)::timestamptz)
RETURNING id, name, prefix, scopes, created_at, expires_at, last_used_at, revoked_at;

-- name: ListAPIKeys :many
SELECT id, name, prefix, scopes, created_at, expires_at, last_used_at, revoked_at
FROM api_keys
WHERE tenant_id = sqlc.arg(tenant_id)::text::uuid
ORDER BY created_at DESC, id DESC;

-- name: RevokeAPIKey :execrows
UPDATE api_keys
SET revoked_at = COALESCE(revoked_at, now())
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id)::text::uuid;

-- name: GetAPIKeySecretHash :one
SELECT secret_hash FROM api_keys WHERE id = sqlc.arg(id);

-- name: MarkAPIKeyUsed :one
UPDATE api_keys
SET last_used_at = now()
WHERE id = sqlc.arg(id)
  AND secret_hash = sqlc.arg(secret_hash)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
RETURNING tenant_id::text, scopes;
