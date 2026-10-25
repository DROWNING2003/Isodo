-- +goose Up
ALTER TABLE injection_rules DROP CONSTRAINT injection_rules_type_check;
ALTER TABLE injection_rules ADD CONSTRAINT injection_rules_type_check
    CHECK (type IN ('http', 'mcp', 'openai', 'anthropic', 'gemini', 'qiniu', 'github'));

-- +goose Down
-- Preserve MCP credentials and their encrypted material as HTTP rules on rollback.
UPDATE injection_rules SET type = 'http' WHERE type = 'mcp';
ALTER TABLE injection_rules DROP CONSTRAINT injection_rules_type_check;
ALTER TABLE injection_rules ADD CONSTRAINT injection_rules_type_check
    CHECK (type IN ('http', 'openai', 'anthropic', 'gemini', 'qiniu', 'github'));
