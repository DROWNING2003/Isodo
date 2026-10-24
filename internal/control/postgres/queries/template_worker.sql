-- name: ExpireTemplateBuildLeases :exec
UPDATE template_builds SET status = 'failed', error_summary = 'Build worker lease expired.',
 lease_token = NULL, lease_expires_at = NULL, updated_at = now(), finished_at = now()
WHERE status IN ('building','validating') AND lease_expires_at < now();

-- name: ClaimTemplateBuild :one
WITH candidate AS (
 SELECT b.id FROM template_builds b JOIN templates t ON t.id = b.template_id
 WHERE b.status = 'queued' AND t.deleted_at IS NULL
 AND NOT EXISTS (SELECT 1 FROM template_builds active WHERE active.template_id = b.template_id AND active.status IN ('building','validating'))
 ORDER BY b.created_at,b.id FOR UPDATE OF b,t SKIP LOCKED LIMIT 1
)
UPDATE template_builds b SET status='building',lease_token=gen_random_uuid(),
 lease_expires_at=now()+make_interval(secs => sqlc.arg(lease_seconds)::int),started_at=now(),updated_at=now()
FROM candidate c,template_builds source,templates target
WHERE b.id=c.id AND source.id=b.source_build_id AND target.id=b.template_id
RETURNING b.id::text AS id,b.template_id::text AS template_id,target.owner_tenant_id::text AS tenant_id,
 b.lease_token::text AS lease_token,source.manifest_digest AS source_digest,
 source.artifact_bucket AS source_bucket,source.artifact_prefix AS source_prefix,
 b.recipe,b.publish_tag,COALESCE(b.expected_tag_build_id::text,'')::text AS expected_tag_build_id;

-- name: RenewTemplateBuild :execrows
UPDATE template_builds SET lease_expires_at=now()+make_interval(secs => sqlc.arg(lease_seconds)::int),updated_at=now()
WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text::uuid
 AND lease_expires_at>now() AND status IN ('building','validating');

-- name: ValidateTemplateBuild :execrows
UPDATE template_builds SET status='validating',updated_at=now()
WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text::uuid
 AND lease_expires_at>now() AND status='building';

-- name: CompleteTemplateBuild :execrows
UPDATE template_builds SET status='ready',manifest_digest=sqlc.arg(digest),artifact_bucket=sqlc.arg(bucket),
 artifact_prefix=sqlc.arg(prefix),finished_at=now(),updated_at=now(),lease_token=NULL,lease_expires_at=NULL
WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text::uuid
 AND lease_expires_at>now() AND status='validating';

-- name: FailTemplateBuild :execrows
UPDATE template_builds SET status='failed',error_summary=sqlc.arg(summary),finished_at=now(),updated_at=now(),lease_token=NULL,lease_expires_at=NULL
WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text::uuid
 AND lease_expires_at>now() AND status IN ('building','validating');

-- name: SetBuildPublicationError :exec
UPDATE template_builds SET publication_error=sqlc.arg(message) WHERE id=sqlc.arg(id)::text::uuid;
