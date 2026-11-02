-- +goose Up
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scopes_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_check
CHECK (cardinality(scopes) BETWEEN 1 AND 6 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write','sandboxes:execute','sandboxes:preview','templates:read','templates:write']::text[]);

-- +goose Down
-- Refuse rollback while any preview-scoped key remains (including revoked keys).
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scopes_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_check
CHECK (cardinality(scopes) BETWEEN 1 AND 5 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write','sandboxes:execute','templates:read','templates:write']::text[]);
