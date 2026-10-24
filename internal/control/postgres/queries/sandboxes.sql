-- name: CreateSandbox :one
INSERT INTO sandboxes (tenant_id, name, metadata, desired_state, observed_state, template_id, template_build_id, template_digest)
VALUES (sqlc.arg(tenant_id)::text::uuid, sqlc.arg(name), sqlc.arg(metadata)::jsonb, 'stopped', 'requested', sqlc.arg(template_id)::text::uuid, sqlc.arg(template_build_id)::text::uuid, sqlc.arg(template_digest)::text)
RETURNING id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest;

-- name: CountSandboxes :one
SELECT count(*)::bigint
FROM sandboxes
WHERE tenant_id = sqlc.arg(tenant_id)::text::uuid AND deleted_at IS NULL;

-- name: ListSandboxes :many
SELECT id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest
FROM sandboxes
WHERE tenant_id = sqlc.arg(tenant_id)::text::uuid AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: GetSandbox :one
SELECT id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest
FROM sandboxes
WHERE id = sqlc.arg(sandbox_id)::text::uuid AND tenant_id = sqlc.arg(tenant_id)::text::uuid AND deleted_at IS NULL;

-- name: UpdateSandbox :one
UPDATE sandboxes
SET name = COALESCE(sqlc.narg('name')::text, name),
    metadata = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = now()
WHERE id = sqlc.arg(sandbox_id)::text::uuid AND tenant_id = sqlc.arg(tenant_id)::text::uuid AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest;

-- name: DeleteSandbox :one
UPDATE sandboxes
SET deleted_at = now(), updated_at = now()
WHERE id = sqlc.arg(sandbox_id)::text::uuid AND tenant_id = sqlc.arg(tenant_id)::text::uuid AND deleted_at IS NULL
RETURNING id::text;
