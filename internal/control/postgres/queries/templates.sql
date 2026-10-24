-- name: CreateTemplate :one
INSERT INTO templates (owner_tenant_id, name, description, visibility)
VALUES (sqlc.arg(tenant_id)::text::uuid, sqlc.arg(name), sqlc.arg(description), sqlc.arg(visibility))
RETURNING id::text, name, description, scope, visibility, created_at, updated_at;

-- name: GetTemplate :one
SELECT id::text, name, description, scope, visibility, created_at, updated_at,
       COALESCE(owner_tenant_id = sqlc.arg(tenant_id)::text::uuid, false)::boolean AS owned
FROM templates
WHERE id = sqlc.arg(id)::text::uuid AND deleted_at IS NULL
  AND (owner_tenant_id = sqlc.arg(tenant_id)::text::uuid OR visibility = 'public');

-- name: ListTemplates :many
SELECT id::text, name, description, scope, visibility, created_at, updated_at,
       COALESCE(owner_tenant_id = sqlc.arg(tenant_id)::text::uuid, false)::boolean AS owned
FROM templates
WHERE deleted_at IS NULL AND (
    (sqlc.arg(list_scope)::text = 'owned' AND owner_tenant_id = sqlc.arg(tenant_id)::text::uuid) OR
    (sqlc.arg(list_scope)::text = 'system' AND scope = 'system') OR
    (sqlc.arg(list_scope)::text = 'public' AND scope = 'personal' AND visibility = 'public') OR
    (sqlc.arg(list_scope)::text = 'available' AND (owner_tenant_id = sqlc.arg(tenant_id)::text::uuid OR visibility = 'public'))
)
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountTemplates :one
SELECT count(*) FROM templates
WHERE deleted_at IS NULL AND (
    (sqlc.arg(list_scope)::text = 'owned' AND owner_tenant_id = sqlc.arg(tenant_id)::text::uuid) OR
    (sqlc.arg(list_scope)::text = 'system' AND scope = 'system') OR
    (sqlc.arg(list_scope)::text = 'public' AND scope = 'personal' AND visibility = 'public') OR
    (sqlc.arg(list_scope)::text = 'available' AND (owner_tenant_id = sqlc.arg(tenant_id)::text::uuid OR visibility = 'public'))
);

-- name: UpdateTemplate :one
UPDATE templates
SET name = COALESCE(sqlc.narg(name)::text, name),
    description = COALESCE(sqlc.narg(description)::text, description),
    visibility = COALESCE(sqlc.narg(visibility)::text, visibility), updated_at = now()
WHERE id = sqlc.arg(id)::text::uuid AND owner_tenant_id = sqlc.arg(tenant_id)::text::uuid AND scope = 'personal' AND deleted_at IS NULL
RETURNING id::text, name, description, scope, visibility, created_at, updated_at;

-- name: LockOwnedTemplate :one
SELECT id::text FROM templates
WHERE id = sqlc.arg(id)::text::uuid AND owner_tenant_id = sqlc.arg(tenant_id)::text::uuid AND scope = 'personal' AND deleted_at IS NULL
FOR UPDATE;

-- name: SoftDeleteTemplate :exec
UPDATE templates SET deleted_at = now(), updated_at = now() WHERE id = sqlc.arg(id)::text::uuid;

-- name: GetAccessibleTemplateBuild :one
SELECT b.id::text AS id, b.template_id::text AS template_id, b.operation_id::text AS operation_id, COALESCE(b.source_build_id::text, '')::text AS source_build_id,
       b.status, b.manifest_digest, b.error_summary, b.publication_error, b.created_at, b.updated_at,
       COALESCE(t.owner_tenant_id = sqlc.arg(tenant_id)::text::uuid, false)::boolean AS owned
FROM template_builds b JOIN templates t ON t.id = b.template_id
WHERE b.id = sqlc.arg(build_id)::text::uuid AND t.id = sqlc.arg(template_id)::text::uuid AND t.deleted_at IS NULL
 AND (t.owner_tenant_id = sqlc.arg(tenant_id)::text::uuid OR (t.visibility = 'public' AND b.status = 'ready'));

-- name: CreateTemplateBuild :one
INSERT INTO template_builds (template_id, source_build_id, recipe, publish_tag, expected_tag_build_id)
VALUES (sqlc.arg(template_id)::text::uuid, sqlc.arg(source_build_id)::text::uuid, sqlc.arg(recipe)::jsonb, sqlc.arg(publish_tag), sqlc.narg(expected_tag_build_id)::text::uuid)
RETURNING id::text, template_id::text, operation_id::text, source_build_id::text, status, manifest_digest, error_summary, publication_error, created_at, updated_at;

-- name: GetAccessibleSourceBuild :one
SELECT b.id::text FROM template_builds b JOIN templates t ON t.id = b.template_id
WHERE b.id = sqlc.arg(build_id)::text::uuid AND b.status = 'ready' AND t.deleted_at IS NULL
 AND (t.owner_tenant_id = sqlc.arg(tenant_id)::text::uuid OR t.visibility = 'public')
FOR SHARE OF t;

-- name: GetTemplateTag :one
SELECT tag.name, tag.build_id::text AS build_id FROM template_tags tag
JOIN templates t ON t.id = tag.template_id JOIN template_builds b ON b.id = tag.build_id
WHERE t.id = sqlc.arg(template_id)::text::uuid AND tag.name = sqlc.arg(tag)::text AND t.deleted_at IS NULL AND b.status = 'ready'
 AND (t.owner_tenant_id = sqlc.arg(tenant_id)::text::uuid OR t.visibility = 'public');

-- name: GetReadyBuild :one
SELECT id::text FROM template_builds WHERE id = sqlc.arg(build_id)::text::uuid AND template_id = sqlc.arg(template_id)::text::uuid AND status = 'ready';

-- name: UpsertTemplateTag :exec
INSERT INTO template_tags (template_id, name, build_id)
VALUES (sqlc.arg(template_id)::text::uuid, sqlc.arg(tag), sqlc.arg(build_id)::text::uuid)
ON CONFLICT (template_id, name) DO UPDATE SET build_id = EXCLUDED.build_id, updated_at = now();

-- name: DeleteTemplateTag :exec
DELETE FROM template_tags WHERE template_id = sqlc.arg(template_id)::text::uuid AND name = sqlc.arg(tag);

-- name: HasActiveTemplateBuild :one
SELECT EXISTS (SELECT 1 FROM template_builds WHERE template_id = sqlc.arg(template_id)::text::uuid AND status IN ('building', 'validating'));

-- name: FailQueuedTemplateBuilds :exec
UPDATE template_builds SET status = 'failed', error_summary = 'Template deleted.', updated_at = now()
WHERE template_id = sqlc.arg(template_id)::text::uuid AND status = 'queued';

-- name: CloneReadyTemplateBuild :one
INSERT INTO template_builds (template_id, source_build_id, recipe, status, manifest_digest, artifact_bucket, artifact_prefix)
SELECT sqlc.arg(template_id)::text::uuid, id, '{}'::jsonb, 'ready', manifest_digest, artifact_bucket, artifact_prefix
FROM template_builds WHERE id = sqlc.arg(source_build_id)::text::uuid AND status = 'ready'
RETURNING id::text;
