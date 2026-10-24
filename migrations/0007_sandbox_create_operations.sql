-- +goose Up
ALTER TABLE sandboxes
    ADD COLUMN node_id text,
    ADD COLUMN ownership_generation bigint NOT NULL DEFAULT 1 CHECK (ownership_generation > 0),
    ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0);

CREATE TABLE sandbox_operations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    sandbox_id uuid NOT NULL REFERENCES sandboxes(id),
    node_id text,
    action text NOT NULL CHECK (action IN ('create')),
    request_hash text NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    stage text NOT NULL DEFAULT 'reserve',
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_owner text,
    lease_expires_at timestamptz,
    error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (sandbox_id, action)
);

CREATE INDEX sandbox_operations_queue_idx
    ON sandbox_operations (created_at, id)
    WHERE status IN ('queued', 'running');

-- +goose Down
DROP TABLE sandbox_operations;
ALTER TABLE sandboxes DROP COLUMN revision, DROP COLUMN ownership_generation, DROP COLUMN node_id;
