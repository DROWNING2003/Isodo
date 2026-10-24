-- +goose Up

CREATE TABLE IF NOT EXISTS tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer text NOT NULL,
    subject text NOT NULL,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    display_name text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_identities_issuer_subject_key UNIQUE (issuer, subject),
    CONSTRAINT user_identities_tenant_key UNIQUE (tenant_id),
    CONSTRAINT user_identities_subject_nonempty CHECK (length(subject) > 0)
);

CREATE TABLE IF NOT EXISTS sandboxes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    desired_state text NOT NULL DEFAULT 'stopped' CHECK (desired_state IN ('running', 'stopped', 'deleted')),
    observed_state text NOT NULL DEFAULT 'requested' CHECK (observed_state IN ('requested', 'preparing', 'starting', 'running', 'stopping', 'stopped', 'deleting', 'deleted', 'error', 'unknown')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS sandboxes_tenant_created_idx
    ON sandboxes (tenant_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;


-- +goose Down
DROP TABLE IF EXISTS sandboxes;
DROP TABLE IF EXISTS user_identities;
DROP TABLE IF EXISTS tenants;
