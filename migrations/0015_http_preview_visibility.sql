-- +goose Up
CREATE TABLE sandbox_port_previews (
 sandbox_id uuid NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
 port integer NOT NULL CHECK (port BETWEEN 1 AND 65535 AND port NOT IN (1024,1080,8080,31024,31080,38080)),
 visibility text NOT NULL CHECK (visibility IN ('public','private')),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 PRIMARY KEY (sandbox_id,port)
);
-- Delete intent and visibility updates serialize on the Sandbox row.
-- +goose StatementBegin
CREATE FUNCTION clear_deleted_sandbox_previews() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.deletion_requested_at IS NOT NULL OR NEW.deleted_at IS NOT NULL THEN
  DELETE FROM sandbox_port_previews WHERE sandbox_id=NEW.id;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER clear_deleted_sandbox_previews AFTER UPDATE OF deletion_requested_at,deleted_at ON sandboxes
 FOR EACH ROW EXECUTE FUNCTION clear_deleted_sandbox_previews();
-- +goose Down
DROP TRIGGER clear_deleted_sandbox_previews ON sandboxes;
DROP FUNCTION clear_deleted_sandbox_previews();
DROP TABLE sandbox_port_previews;
