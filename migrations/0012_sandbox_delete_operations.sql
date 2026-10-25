-- +goose Up
ALTER TABLE sandboxes ADD COLUMN deletion_requested_at timestamptz;
-- Old deleted_at only represented a logical deletion, not proven Node cleanup.
UPDATE sandboxes SET deletion_requested_at = deleted_at, deleted_at = NULL WHERE deleted_at IS NOT NULL;
ALTER TABLE sandbox_operations DROP CONSTRAINT sandbox_operations_action_check;
ALTER TABLE sandbox_operations ADD CONSTRAINT sandbox_operations_action_check CHECK (action IN ('create', 'start', 'stop', 'delete'));

-- +goose Down
DELETE FROM sandbox_operations WHERE action = 'delete';
ALTER TABLE sandbox_operations DROP CONSTRAINT sandbox_operations_action_check;
ALTER TABLE sandbox_operations ADD CONSTRAINT sandbox_operations_action_check CHECK (action IN ('create', 'start', 'stop'));
ALTER TABLE sandboxes DROP COLUMN deletion_requested_at;
