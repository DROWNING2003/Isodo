-- +goose Up
CREATE TABLE mcp_custom_servers (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
 description text NOT NULL DEFAULT '', endpoint text NOT NULL,
 auth_kind text NOT NULL CHECK(auth_kind IN ('none','bearer')),
 nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 tools jsonb NOT NULL DEFAULT '[]',
 status text NOT NULL DEFAULT 'connected' CHECK(status IN ('connected','expired','disconnected','failed')),
 version bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id)
);
CREATE TABLE mcp_custom_bindings (
 sandbox_binding_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 sandbox_id uuid NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
 custom_server_id uuid NOT NULL,
 capability_hash bytea NOT NULL,
 sync_status text NOT NULL DEFAULT 'pending' CHECK(sync_status IN ('pending','applied','failed')),
 FOREIGN KEY(tenant_id,custom_server_id) REFERENCES mcp_custom_servers(tenant_id,id) ON DELETE CASCADE,
 UNIQUE(sandbox_id,custom_server_id)
);
CREATE TRIGGER validate_custom_mcp_owner BEFORE INSERT ON mcp_custom_bindings
 FOR EACH ROW EXECUTE FUNCTION validate_mcp_binding_owner();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cleanup_mcp_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.desired_state='deleted' OR NEW.deleted_at IS NOT NULL OR NEW.deletion_requested_at IS NOT NULL THEN
  DELETE FROM mcp_sandbox_bindings WHERE sandbox_id=NEW.id;
  DELETE FROM mcp_custom_bindings WHERE sandbox_id=NEW.id;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cleanup_mcp_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.desired_state='deleted' OR NEW.deleted_at IS NOT NULL OR NEW.deletion_requested_at IS NOT NULL THEN
  DELETE FROM mcp_sandbox_bindings WHERE sandbox_id=NEW.id;
 END IF;
 RETURN NEW;
END; $$;
-- +goose StatementEnd
DROP TABLE mcp_custom_bindings;
DROP TABLE mcp_custom_servers;
