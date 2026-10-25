-- +goose Up
ALTER TABLE sandbox_operations DROP CONSTRAINT IF EXISTS sandbox_operations_sandbox_id_action_key;
ALTER TABLE sandbox_operations DROP CONSTRAINT IF EXISTS sandbox_operations_action_check;
ALTER TABLE sandbox_operations
    ADD CONSTRAINT sandbox_operations_action_check CHECK (action IN ('create', 'start', 'stop'));

-- +goose Down
DELETE FROM sandbox_operations WHERE action IN ('start', 'stop');
ALTER TABLE sandbox_operations DROP CONSTRAINT IF EXISTS sandbox_operations_action_check;
ALTER TABLE sandbox_operations
    ADD CONSTRAINT sandbox_operations_action_check CHECK (action IN ('create'));
ALTER TABLE sandbox_operations ADD CONSTRAINT sandbox_operations_sandbox_id_action_key UNIQUE (sandbox_id, action);
