-- +goose Up
-- Account MCP authorization is independent of favorites and sandbox lifetimes.
CREATE TABLE mcp_catalog (
 market_mcp_id text PRIMARY KEY,
 name text NOT NULL, provider text NOT NULL, description text NOT NULL,
 endpoint text NOT NULL, transport text NOT NULL, auth_kind text NOT NULL
);
INSERT INTO mcp_catalog VALUES ('github','GitHub','GitHub','访问 GitHub 仓库、Issue、Pull Request 等资源。','https://api.githubcopilot.com/mcp/readonly','streamable-http','oauth2');
CREATE TABLE mcp_favorites (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 market_mcp_id text NOT NULL REFERENCES mcp_catalog,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,market_mcp_id)
);
CREATE TABLE mcp_oauth_connections (
 oauth_connection_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 market_mcp_id text NOT NULL REFERENCES mcp_catalog,
 status text NOT NULL CHECK (status IN ('connected','expired','disconnected','failed')),
 nonce bytea, ciphertext bytea,
 tools jsonb NOT NULL DEFAULT '[]',
 expires_at timestamptz, version bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (tenant_id,market_mcp_id), UNIQUE(tenant_id,oauth_connection_id)
);
CREATE TABLE mcp_oauth_transactions (
 transaction_id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 market_mcp_id text NOT NULL REFERENCES mcp_catalog,
 state_hash bytea NOT NULL UNIQUE, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','connected','failed','cancelled')),
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mcp_oauth_pending ON mcp_oauth_transactions(tenant_id,expires_at) WHERE status='pending';
CREATE TABLE mcp_sandbox_bindings (
 sandbox_binding_id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id),
 sandbox_id uuid NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
 market_mcp_id text NOT NULL REFERENCES mcp_catalog,
 oauth_connection_id uuid NOT NULL,
 capability_hash bytea NOT NULL,
 sync_status text NOT NULL DEFAULT 'pending' CHECK(sync_status IN ('pending','applied','failed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(tenant_id,oauth_connection_id) REFERENCES mcp_oauth_connections(tenant_id,oauth_connection_id),
 UNIQUE(sandbox_id,market_mcp_id)
);
-- Sandboxes use tombstones; remove only sandbox bindings when deletion begins.
-- +goose StatementBegin
CREATE FUNCTION cleanup_mcp_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.desired_state='deleted' OR NEW.deleted_at IS NOT NULL OR NEW.deletion_requested_at IS NOT NULL THEN
  DELETE FROM mcp_sandbox_bindings WHERE sandbox_id=NEW.id;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
CREATE TRIGGER cleanup_mcp_bindings AFTER UPDATE OF desired_state,deleted_at,deletion_requested_at ON sandboxes
 FOR EACH ROW EXECUTE FUNCTION cleanup_mcp_bindings();

-- +goose StatementBegin
CREATE FUNCTION validate_mcp_binding_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM sandboxes WHERE id=NEW.sandbox_id AND tenant_id=NEW.tenant_id
  AND desired_state!='deleted' AND deletion_requested_at IS NULL AND deleted_at IS NULL FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'sandbox binding owner or lifecycle invalid'; END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
CREATE TRIGGER validate_mcp_binding_owner BEFORE INSERT ON mcp_sandbox_bindings
 FOR EACH ROW EXECUTE FUNCTION validate_mcp_binding_owner();
-- +goose Down
DROP TRIGGER validate_mcp_binding_owner ON mcp_sandbox_bindings;
DROP FUNCTION validate_mcp_binding_owner();
DROP TRIGGER cleanup_mcp_bindings ON sandboxes;
DROP FUNCTION cleanup_mcp_bindings();
DROP TABLE mcp_sandbox_bindings;
DROP TABLE mcp_oauth_transactions;
DROP TABLE mcp_oauth_connections;
DROP TABLE mcp_favorites;
DROP TABLE mcp_catalog;
