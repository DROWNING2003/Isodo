-- +goose Up
INSERT INTO mcp_catalog VALUES
 ('notion','Notion','Notion','只读搜索和读取已授权的 Notion 页面与数据库。','https://mcp.notion.com/mcp','streamable-http','oauth2'),
 ('supabase','Supabase','Supabase','限定一个测试项目，读取表结构、扩展、诊断与官方文档。','https://mcp.supabase.com/mcp','streamable-http','oauth2');
CREATE TABLE mcp_oauth_registrations (
 provider text NOT NULL REFERENCES mcp_catalog(market_mcp_id), issuer text NOT NULL, redirect_uri text NOT NULL,
 nonce bytea NOT NULL, ciphertext bytea NOT NULL, client_id_hash bytea NOT NULL,
 status text NOT NULL CHECK(status IN ('active','invalid')),
 updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(provider,issuer,redirect_uri)
);
ALTER TABLE mcp_oauth_connections ADD COLUMN project_ref text CHECK(project_ref IS NULL OR project_ref ~ '^[a-z]{20}$');
ALTER TABLE mcp_oauth_connections ADD COLUMN workspace_id text CHECK(length(workspace_id)<=128), ADD COLUMN workspace_name text CHECK(length(workspace_name)<=256);
ALTER TABLE mcp_sandbox_bindings ADD COLUMN project_ref text CHECK(project_ref IS NULL OR project_ref ~ '^[a-z]{20}$');
-- +goose Down
DELETE FROM mcp_sandbox_bindings WHERE market_mcp_id IN ('notion','supabase');
DELETE FROM mcp_oauth_transactions WHERE market_mcp_id IN ('notion','supabase');
DELETE FROM mcp_oauth_connections WHERE market_mcp_id IN ('notion','supabase');
DELETE FROM mcp_favorites WHERE market_mcp_id IN ('notion','supabase');
DROP TABLE mcp_oauth_registrations;
DELETE FROM mcp_catalog WHERE market_mcp_id IN ('notion','supabase');
ALTER TABLE mcp_sandbox_bindings DROP COLUMN project_ref;
ALTER TABLE mcp_oauth_connections DROP COLUMN project_ref, DROP COLUMN workspace_id, DROP COLUMN workspace_name;
