-- +goose Up
CREATE TABLE IF NOT EXISTS injection_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    type text NOT NULL CHECK (type IN ('http', 'openai', 'anthropic', 'gemini', 'qiniu', 'github')),
    base_url text NOT NULL DEFAULT '',
    header_name text NOT NULL DEFAULT '',
    if_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
    if_queries jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_nonce bytea NOT NULL CHECK (octet_length(secret_nonce) = 12),
    secret_ciphertext bytea NOT NULL,
    secret_hint text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS injection_rules_tenant_created_idx ON injection_rules(tenant_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS injection_rules;
