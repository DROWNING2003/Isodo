-- +goose Up
-- 0019 was already deployed; keep its checksum intact and retire this provider
-- with a forward migration. Revocation removes durable credentials/capabilities.
DELETE FROM mcp_sandbox_bindings WHERE market_mcp_id = 'supabase';
DELETE FROM mcp_oauth_transactions WHERE market_mcp_id = 'supabase';
DELETE FROM mcp_oauth_connections WHERE market_mcp_id = 'supabase';
DELETE FROM mcp_favorites WHERE market_mcp_id = 'supabase';
DELETE FROM mcp_oauth_registrations WHERE provider = 'supabase';
DELETE FROM mcp_catalog WHERE market_mcp_id = 'supabase';
-- +goose Down
-- Restore only the catalog entry; never resurrect revoked credentials or grants.
INSERT INTO mcp_catalog VALUES
 ('supabase','Supabase','Supabase','限定一个测试项目，读取表结构、扩展、诊断与官方文档。','https://mcp.supabase.com/mcp','streamable-http','oauth2')
ON CONFLICT DO NOTHING;
