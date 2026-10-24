-- name: ResolveSandboxTemplate :one
SELECT t.id::text AS template_id,b.id::text AS build_id,b.manifest_digest
FROM templates t JOIN template_tags tag ON tag.template_id=t.id JOIN template_builds b ON b.id=tag.build_id
LEFT JOIN system_template_catalog c ON c.template_id=t.id
WHERE (t.id::text=sqlc.arg(identifier)::text OR c.name=sqlc.arg(identifier)::text)
 AND tag.name=sqlc.arg(tag)::text AND t.deleted_at IS NULL AND b.status='ready'
 AND (t.owner_tenant_id=sqlc.arg(tenant_id)::text::uuid OR t.visibility='public')
FOR SHARE OF t,tag,b;
