-- +goose Up
CREATE TABLE templates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_tenant_id uuid REFERENCES tenants(id),
    scope text NOT NULL DEFAULT 'personal' CHECK (scope IN ('system', 'personal')),
    visibility text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
    description text NOT NULL DEFAULT '' CHECK (octet_length(description) <= 2048),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK ((scope = 'personal' AND owner_tenant_id IS NOT NULL) OR
           (scope = 'system' AND owner_tenant_id IS NULL AND visibility = 'public'))
);
CREATE INDEX templates_owner_created_idx ON templates(owner_tenant_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX templates_public_created_idx ON templates(created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility = 'public';

CREATE TABLE template_builds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id uuid NOT NULL REFERENCES templates(id),
    operation_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    source_build_id uuid REFERENCES template_builds(id),
    recipe jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(recipe) = 'object'),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'building', 'validating', 'ready', 'failed')),
    manifest_digest text CHECK (manifest_digest ~ '^[0-9a-f]{64}$'),
    artifact_bucket text,
    artifact_prefix text,
    error_summary text NOT NULL DEFAULT '',
    lease_token uuid,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (template_id, id),
    CHECK (status <> 'ready' OR (manifest_digest IS NOT NULL AND artifact_bucket IS NOT NULL AND artifact_prefix IS NOT NULL))
);
CREATE INDEX template_builds_queue_idx ON template_builds(created_at, id) WHERE status = 'queued';
CREATE TABLE template_tags (
    template_id uuid NOT NULL REFERENCES templates(id),
    name text NOT NULL CHECK (name ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$'),
    build_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, name),
    FOREIGN KEY (template_id, build_id) REFERENCES template_builds(template_id, id)
);
CREATE TABLE system_template_catalog (
    name text PRIMARY KEY,
    template_id uuid NOT NULL,
    build_id uuid NOT NULL,
    FOREIGN KEY (template_id, build_id) REFERENCES template_builds(template_id, id)
);

-- +goose Down
DROP TABLE system_template_catalog;
DROP TABLE template_tags;
DROP TABLE template_builds;
DROP TABLE templates;
