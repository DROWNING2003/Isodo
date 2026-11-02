-- +goose Up
-- Command execution is a separate capability from Sandbox metadata writes.
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scopes_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_check
CHECK (cardinality(scopes) BETWEEN 1 AND 5 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write','sandboxes:execute','templates:read','templates:write']::text[]);

-- +goose Down
-- Refuse rollback when a key still has the execution scope.
ALTER TABLE api_keys DROP CONSTRAINT api_keys_scopes_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_check
CHECK (cardinality(scopes) BETWEEN 1 AND 4 AND scopes <@ ARRAY['sandboxes:read','sandboxes:write','templates:read','templates:write']::text[]);
