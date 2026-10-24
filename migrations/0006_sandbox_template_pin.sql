-- +goose Up
ALTER TABLE sandboxes
 ADD COLUMN template_id uuid,
 ADD COLUMN template_build_id uuid,
 ADD COLUMN template_digest text,
 ADD CONSTRAINT sandbox_template_build_fk FOREIGN KEY(template_id,template_build_id) REFERENCES template_builds(template_id,id),
 ADD CONSTRAINT sandbox_template_pin_complete CHECK (
  (template_id IS NULL AND template_build_id IS NULL AND template_digest IS NULL) OR
  (template_id IS NOT NULL AND template_build_id IS NOT NULL AND template_digest IS NOT NULL AND template_digest ~ '^[0-9a-f]{64}$')
 );
-- Existing metadata-only sandboxes remain unpinned; do not invent a base build.
-- +goose Down
ALTER TABLE sandboxes DROP COLUMN template_digest, DROP COLUMN template_build_id, DROP COLUMN template_id;
