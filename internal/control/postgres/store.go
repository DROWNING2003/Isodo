package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/DROWNING2003/Isodo/internal/control/operation"
	"github.com/DROWNING2003/Isodo/internal/control/postgres/dbsqlc"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) ResolveTenant(ctx context.Context, issuer, subject, displayName string) (string, error) {
	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	// Serializing the first-login transaction by the stable identity avoids a
	// duplicate tenant without relying on email or client-supplied tenant data.
	if _, err := transaction.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended(json_build_array($1::text, $2::text)::text, 0))`, issuer, subject,
	); err != nil {
		return "", err
	}
	var tenantID string
	err = transaction.QueryRow(ctx,
		`SELECT tenant_id::text FROM user_identities WHERE issuer = $1 AND subject = $2`, issuer, subject,
	).Scan(&tenantID)
	if err == nil {
		if _, err := transaction.Exec(ctx,
			`UPDATE user_identities SET display_name = NULLIF($3, ''), updated_at = now() WHERE issuer = $1 AND subject = $2`, issuer, subject, displayName,
		); err != nil {
			return "", err
		}
		if err := transaction.Commit(ctx); err != nil {
			return "", err
		}
		return tenantID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if err := transaction.QueryRow(ctx, `INSERT INTO tenants DEFAULT VALUES RETURNING id::text`).Scan(&tenantID); err != nil {
		return "", err
	}
	if _, err := transaction.Exec(ctx,
		`INSERT INTO user_identities (issuer, subject, tenant_id, display_name)
		 VALUES ($1, $2, $3::uuid, NULLIF($4, ''))`, issuer, subject, tenantID, displayName,
	); err != nil {
		return "", err
	}
	if err := transaction.Commit(ctx); err != nil {
		return "", err
	}
	return tenantID, nil
}

func (s *Store) CreateSandbox(ctx context.Context, tenantID, name string, metadata json.RawMessage, reference string) (sandbox.Sandbox, error) {
	localLima := reference == sandbox.LocalLimaTemplateReference
	identifier, tag := "", ""
	if !localLima {
		var err error
		identifier, tag, err = sandbox.ParseTemplateReference(reference)
		if err != nil {
			return sandbox.Sandbox{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var artifactBucket, artifactPrefix, templateID, buildID, digest string
	var item sandbox.Sandbox
	if localLima {
		item, err = scanSandbox(tx.QueryRow(ctx,
			`INSERT INTO sandboxes (tenant_id, name, metadata, desired_state, observed_state)
			 VALUES ($1::uuid, $2, $3::jsonb, 'stopped', 'requested')
			 RETURNING id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest`,
			tenantID, name, metadata,
		))
		if err != nil {
			return sandbox.Sandbox{}, err
		}
	} else {
		resolved, resolveErr := dbsqlc.New(tx).ResolveSandboxTemplate(ctx, dbsqlc.ResolveSandboxTemplateParams{Identifier: identifier, Tag: tag, TenantID: tenantID})
		if errors.Is(resolveErr, pgx.ErrNoRows) {
			return sandbox.Sandbox{}, sandbox.ErrTemplateUnavailable
		}
		if resolveErr != nil {
			return sandbox.Sandbox{}, resolveErr
		}
		if resolved.ManifestDigest == nil {
			return sandbox.Sandbox{}, errors.New("ready template has no manifest digest")
		}
		if err := tx.QueryRow(ctx,
			`SELECT artifact_bucket, artifact_prefix FROM template_builds WHERE id = $1::uuid`, resolved.BuildID,
		).Scan(&artifactBucket, &artifactPrefix); err != nil {
			return sandbox.Sandbox{}, err
		}
		row, createErr := dbsqlc.New(tx).CreateSandbox(ctx, dbsqlc.CreateSandboxParams{
			TenantID: tenantID, Name: name, Metadata: metadata, TemplateID: resolved.TemplateID, TemplateBuildID: resolved.BuildID, TemplateDigest: *resolved.ManifestDigest,
		})
		if createErr != nil {
			return sandbox.Sandbox{}, createErr
		}
		templateID, buildID, digest = resolved.TemplateID, resolved.BuildID, *resolved.ManifestDigest
		item = sandbox.Sandbox{ID: row.ID, TenantID: row.TenantID, Name: row.Name, Metadata: json.RawMessage(row.Metadata), ObservedState: row.ObservedState, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, TemplateID: new(row.TemplateID), TemplateBuildID: new(row.TemplateBuildID), TemplateDigest: row.TemplateDigest}
	}
	payload := operation.CreatePayload{
		TemplateRef: reference, TemplateID: templateID, TemplateBuildID: buildID,
		TemplateDigest: digest, ArtifactBucket: artifactBucket, ArtifactPrefix: artifactPrefix,
	}
	if !localLima {
		payload.Resources = new(operation.Resources)
		*payload.Resources = operation.DefaultResources()
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	requestHash := sha256.Sum256([]byte(strings.Join([]string{item.ID, "create", string(payloadJSON), "1", "1"}, "\x00")))
	operationID := uuid.NewString()
	if _, err := tx.Exec(ctx,
		`UPDATE sandboxes SET desired_state = 'running' WHERE id = $1::uuid`, item.ID,
	); err != nil {
		return sandbox.Sandbox{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sandbox_operations (id, tenant_id, sandbox_id, action, request_hash, payload)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'create', $4, $5::jsonb)`,
		operationID, tenantID, item.ID, fmt.Sprintf("%x", requestHash), payloadJSON,
	); err != nil {
		return sandbox.Sandbox{}, err
	}
	item.DesiredState = "running"
	return item, tx.Commit(ctx)
}

func (s *Store) ClaimSandboxCreate(ctx context.Context, workerID, nodeID string) (operation.CreateJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return operation.CreateJob{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var job operation.CreateJob
	var payloadJSON []byte
	err = tx.QueryRow(ctx,
		`WITH candidate AS (
			SELECT id FROM sandbox_operations
			WHERE action = 'create' AND status IN ('queued', 'running')
			  AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE sandbox_operations op
		SET status = 'running', stage = 'starting', attempt = attempt + 1,
		    node_id = $2, lease_owner = $1, lease_expires_at = now() + interval '30 seconds', updated_at = now()
		FROM candidate
		WHERE op.id = candidate.id
		RETURNING op.id::text, op.sandbox_id::text, op.tenant_id::text, op.request_hash, op.payload`,
		workerID, nodeID,
	).Scan(&job.OperationID, &job.SandboxID, &job.TenantID, &job.RequestHash, &payloadJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return operation.CreateJob{}, operation.ErrNoCreateOperation
	}
	if err != nil {
		return operation.CreateJob{}, err
	}
	var payload operation.CreatePayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return operation.CreateJob{}, err
	}
	job.TemplateRef = payload.TemplateRef
	job.TemplateID = payload.TemplateID
	job.TemplateBuildID = payload.TemplateBuildID
	job.TemplateDigest = payload.TemplateDigest
	job.ArtifactBucket = payload.ArtifactBucket
	job.ArtifactPrefix = payload.ArtifactPrefix
	if payload.Resources != nil {
		job.Resources = *payload.Resources
	}
	job.WorkerID = workerID
	job.OwnershipGeneration = 1
	job.Revision = 1
	if _, err := tx.Exec(ctx, `UPDATE sandboxes SET node_id = $2, updated_at = now() WHERE id = $1::uuid`, job.SandboxID, nodeID); err != nil {
		return operation.CreateJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return operation.CreateJob{}, err
	}
	return job, nil
}

func (s *Store) ExtendSandboxCreateLease(ctx context.Context, operationID, workerID string) error {
	command, err := s.pool.Exec(ctx,
		`UPDATE sandbox_operations SET lease_expires_at = now() + interval '30 seconds', updated_at = now()
		 WHERE id = $1::uuid AND lease_owner = $2 AND status = 'running'`, operationID, workerID,
	)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("sandbox create operation lease was lost")
	}
	return nil
}

func (s *Store) CompleteSandboxCreate(ctx context.Context, job operation.CreateJob, errorCode string) error {
	status, observed, desired := "succeeded", "running", "running"
	if errorCode != "" {
		status, observed, desired = "failed", "error", "stopped"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	command, err := tx.Exec(ctx,
		`UPDATE sandbox_operations SET status = $2, stage = CASE WHEN $2 = 'succeeded' THEN 'guest_ready' ELSE stage END,
		 error_code = $3, lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND sandbox_id = $4::uuid AND lease_owner = $5 AND status = 'running'`, job.OperationID, status, errorCode, job.SandboxID, job.WorkerID,
	)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("sandbox create operation lease was lost")
	}
	command, err = tx.Exec(ctx,
		`UPDATE sandboxes SET desired_state = $2, observed_state = $3, updated_at = now()
		 WHERE id = $1::uuid AND tenant_id = $4::uuid`, job.SandboxID, desired, observed, job.TenantID,
	)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return sandbox.ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) RetrySandboxCreate(ctx context.Context, job operation.CreateJob) error {
	command, err := s.pool.Exec(ctx,
		`UPDATE sandbox_operations SET status = 'queued', lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND sandbox_id = $2::uuid AND lease_owner = $3 AND status = 'running'`, job.OperationID, job.SandboxID, job.WorkerID,
	)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("sandbox create operation lease was lost")
	}
	return nil
}

func (s *Store) ListSandboxes(ctx context.Context, tenantID string, limit, offset int) ([]sandbox.Sandbox, int64, error) {
	var total int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM sandboxes WHERE tenant_id = $1::uuid AND deleted_at IS NULL`, tenantID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest
		 FROM sandboxes
		 WHERE tenant_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2 OFFSET $3`, tenantID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]sandbox.Sandbox, 0)
	for rows.Next() {
		item, err := scanSandbox(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Store) GetSandbox(ctx context.Context, tenantID, sandboxID string) (sandbox.Sandbox, error) {
	item, err := scanSandbox(s.pool.QueryRow(ctx,
		`SELECT id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest
		 FROM sandboxes
		 WHERE id = $1::uuid AND tenant_id = $2::uuid AND deleted_at IS NULL`, sandboxID, tenantID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	return item, err
}

func (s *Store) UpdateSandbox(ctx context.Context, tenantID, sandboxID string, patch sandbox.Patch) (sandbox.Sandbox, error) {
	var metadata any
	if patch.Metadata != nil {
		metadata = []byte(*patch.Metadata)
	}
	var name any
	if patch.Name != nil {
		name = *patch.Name
	}
	item, err := scanSandbox(s.pool.QueryRow(ctx,
		`UPDATE sandboxes
		 SET name = COALESCE($3::text, name),
		     metadata = COALESCE($4::jsonb, metadata),
		     updated_at = now()
		 WHERE id = $1::uuid AND tenant_id = $2::uuid AND deleted_at IS NULL
		 RETURNING id::text, tenant_id::text, name, metadata, desired_state, observed_state, created_at, updated_at, template_id::text, template_build_id::text, template_digest`,
		sandboxID, tenantID, name, metadata,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	return item, err
}

func (s *Store) DeleteSandbox(ctx context.Context, tenantID, sandboxID string) error {
	var deletedID string
	err := s.pool.QueryRow(ctx,
		`UPDATE sandboxes SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND tenant_id = $2::uuid AND deleted_at IS NULL
		 RETURNING id::text`, sandboxID, tenantID,
	).Scan(&deletedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sandbox.ErrNotFound
	}
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSandbox(row rowScanner) (sandbox.Sandbox, error) {
	var item sandbox.Sandbox
	var metadata []byte
	if err := row.Scan(
		&item.ID,
		&item.TenantID,
		&item.Name,
		&metadata,
		&item.DesiredState,
		&item.ObservedState,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.TemplateID, &item.TemplateBuildID, &item.TemplateDigest,
	); err != nil {
		return sandbox.Sandbox{}, err
	}
	item.Metadata = json.RawMessage(metadata)
	return item, nil
}
