-- +goose Up
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scopes_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_check
CHECK (cardinality(scopes) BETWEEN 1 AND 4 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write','templates:read','templates:write']::text[]);

-- +goose Down
-- Refuse rollback while template-scoped keys exist; do not silently widen or change credentials.
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scopes_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_check
CHECK (cardinality(scopes) BETWEEN 1 AND 2 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write']::text[]);
