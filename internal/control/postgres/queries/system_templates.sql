-- name: LockSystemTemplatePublication :exec
SELECT pg_advisory_xact_lock(hashtextextended('isodo-system-template:' || sqlc.arg(name)::text, 0));

-- name: GetSystemTemplatePublication :one
SELECT c.template_id::text, c.build_id::text, b.manifest_digest, b.artifact_bucket
FROM system_template_catalog c JOIN template_builds b ON b.id=c.build_id
WHERE c.name=sqlc.arg(name)::text;

-- name: CreateSystemTemplate :one
INSERT INTO templates(scope,visibility,name,description) VALUES('system','public',sqlc.arg(name),sqlc.arg(description)) RETURNING id::text;

-- name: CreateSystemTemplateBuild :one
INSERT INTO template_builds(template_id,status,manifest_digest,artifact_bucket,artifact_prefix,started_at,finished_at)
VALUES(sqlc.arg(template_id)::text::uuid,'ready',sqlc.arg(digest),sqlc.arg(bucket),sqlc.arg(prefix),now(),now()) RETURNING id::text;

-- name: PublishSystemTemplateCatalog :exec
INSERT INTO system_template_catalog(name,template_id,build_id) VALUES(sqlc.arg(name),sqlc.arg(template_id)::text::uuid,sqlc.arg(build_id)::text::uuid)
ON CONFLICT(name) DO UPDATE SET build_id=EXCLUDED.build_id;
