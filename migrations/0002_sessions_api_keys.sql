-- +goose Up
CREATE TABLE IF NOT EXISTS oauth_states (
    state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash) = 32),
    nonce text NOT NULL,
    code_verifier text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS web_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    csrf_token text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS web_sessions_expiry_idx ON web_sessions(expires_at);
CREATE TABLE IF NOT EXISTS api_keys (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    secret_hash bytea NOT NULL CHECK (octet_length(secret_hash) = 32),
    prefix text NOT NULL,
    scopes text[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 2 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write']::text[]),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX IF NOT EXISTS api_keys_tenant_created_idx ON api_keys(tenant_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS web_sessions;
DROP TABLE IF EXISTS oauth_states;
