package postgres

import (
	"context"
	"errors"

	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/jackc/pgx/v5"
)

func (s *Store) LookupPreviewSandbox(ctx context.Context, id string) (sandbox.Sandbox, error) {
	var tenant string
	err := s.pool.QueryRow(ctx, `SELECT tenant_id::text FROM sandboxes WHERE id=$1::uuid AND deleted_at IS NULL AND deletion_requested_at IS NULL`, id).Scan(&tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	return s.GetSandbox(ctx, tenant, id)
}
func (s *Store) GetPortPreview(ctx context.Context, tenant, id string, port uint16) (preview.PortPolicy, error) {
	var p preview.PortPolicy
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(p.visibility,'public'),COALESCE(p.revision,0)
 FROM sandboxes s LEFT JOIN sandbox_port_previews p ON p.sandbox_id=s.id AND p.port=$3
 WHERE s.id=$1::uuid AND s.tenant_id=$2::uuid AND s.deleted_at IS NULL AND s.deletion_requested_at IS NULL`, id, tenant, int(port)).Scan(&p.Visibility, &p.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = sandbox.ErrNotFound
	}
	return p, err
}
func (s *Store) SetPortPreview(ctx context.Context, tenant, id string, port uint16, visibility string) (preview.PortPolicy, error) {
	if !preview.ValidHTTPPort(port) || (visibility != "public" && visibility != "private") {
		return preview.PortPolicy{}, sandbox.ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return preview.PortPolicy{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var locked string
	err = tx.QueryRow(ctx, `SELECT id::text FROM sandboxes WHERE id=$1::uuid AND tenant_id=$2::uuid AND deleted_at IS NULL AND deletion_requested_at IS NULL FOR UPDATE`, id, tenant).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		err = sandbox.ErrNotFound
	}
	if err != nil {
		return preview.PortPolicy{}, err
	}
	var p preview.PortPolicy
	err = tx.QueryRow(ctx, `INSERT INTO sandbox_port_previews(sandbox_id,port,visibility) VALUES($1::uuid,$2,$3)
 ON CONFLICT(sandbox_id,port) DO UPDATE SET visibility=EXCLUDED.visibility,
 revision=sandbox_port_previews.revision+CASE WHEN sandbox_port_previews.visibility<>EXCLUDED.visibility THEN 1 ELSE 0 END
 RETURNING visibility,revision`, id, int(port), visibility).Scan(&p.Visibility, &p.Revision)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}
