-- +goose Up
ALTER TABLE template_builds
    ADD COLUMN publish_tag text NOT NULL DEFAULT 'default' CHECK (publish_tag ~ '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$'),
    ADD COLUMN expected_tag_build_id uuid REFERENCES template_builds(id),
    ADD COLUMN publication_error text NOT NULL DEFAULT '',
    ADD COLUMN started_at timestamptz,
    ADD COLUMN finished_at timestamptz;

CREATE UNIQUE INDEX template_builds_one_active_idx ON template_builds(template_id) WHERE status IN ('building','validating');

-- +goose Down
DROP INDEX template_builds_one_active_idx;
ALTER TABLE template_builds DROP COLUMN finished_at, DROP COLUMN started_at,
    DROP COLUMN publication_error, DROP COLUMN expected_tag_build_id, DROP COLUMN publish_tag;
