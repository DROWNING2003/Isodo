-- Run as the migration owner with psql -v app_role=YOUR_CONTROL_ROLE.
-- The runtime role may read the platform catalog, but cannot modify it.
\if :{?app_role}
BEGIN;
GRANT SELECT ON TABLE mcp_catalog TO :"app_role";
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
 mcp_favorites, mcp_oauth_connections, mcp_oauth_transactions, mcp_sandbox_bindings
 TO :"app_role";
-- PR #25 preview policies are also used by the Sandbox deletion trigger.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE sandbox_port_previews TO :"app_role";
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE mcp_custom_servers, mcp_custom_bindings TO :"app_role";
COMMIT;
\else
\echo 'app_role is required; no grants applied'
\quit 1
\endif
