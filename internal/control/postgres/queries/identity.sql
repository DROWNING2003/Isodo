-- name: LockIdentity :exec
SELECT pg_advisory_xact_lock(hashtextextended(json_build_array(sqlc.arg(issuer)::text, sqlc.arg(subject)::text)::text, 0));

-- name: GetIdentityTenant :one
SELECT tenant_id::text
FROM user_identities
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: UpdateIdentityDisplayName :exec
UPDATE user_identities
SET display_name = NULLIF(sqlc.arg(display_name)::text, ''), updated_at = now()
WHERE issuer = sqlc.arg(issuer) AND subject = sqlc.arg(subject);

-- name: CreateTenant :one
INSERT INTO tenants DEFAULT VALUES
RETURNING id::text;

-- name: CreateIdentity :exec
INSERT INTO user_identities (issuer, subject, tenant_id, display_name)
VALUES (sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(tenant_id)::text::uuid, NULLIF(sqlc.arg(display_name)::text, ''));
