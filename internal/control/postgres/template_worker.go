package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/postgres/dbsqlc"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ template.BuildQueue = (*Store)(nil)

func leaseSeconds(lease time.Duration) int32 { return int32(lease / time.Second) }
func (s *Store) ClaimTemplateBuild(ctx context.Context, lease time.Duration) (*template.BuildJob, error) {
	q := dbsqlc.New(s.pool)
	if err := q.ExpireTemplateBuildLeases(ctx); err != nil {
		return nil, err
	}
	row, err := q.ClaimTemplateBuild(ctx, leaseSeconds(lease))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if pgError, ok := errors.AsType[*pgconn.PgError](err); ok && pgError.Code == "23505" && pgError.ConstraintName == "template_builds_one_active_idx" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job := &template.BuildJob{ID: row.ID, TemplateID: row.TemplateID, TenantID: row.TenantID, LeaseToken: row.LeaseToken, PublishTag: row.PublishTag, ExpectedTagBuildID: row.ExpectedTagBuildID}
	if row.SourceDigest == nil || row.SourceBucket == nil || row.SourcePrefix == nil {
		_ = s.FailTemplateBuild(ctx, *job, "Source build lacks a published artifact.")
		return nil, errors.New("source build lacks a published artifact")
	}
	job.SourceDigest = *row.SourceDigest
	job.SourceBucket = *row.SourceBucket
	job.SourcePrefix = *row.SourcePrefix
	if err = json.Unmarshal(row.Recipe, &job.Recipe); err != nil {
		_ = s.FailTemplateBuild(ctx, *job, "Stored build recipe is invalid.")
		return nil, fmt.Errorf("decode queued recipe: %w", err)
	}
	return job, nil
}
func checkBuildLease(affected int64, err error) error {
	if err != nil {
		return err
	}
	if affected != 1 {
		return template.ErrLeaseLost
	}
	return nil
}
func (s *Store) RenewTemplateBuild(ctx context.Context, job template.BuildJob, lease time.Duration) error {
	n, err := dbsqlc.New(s.pool).RenewTemplateBuild(ctx, dbsqlc.RenewTemplateBuildParams{ID: job.ID, LeaseToken: job.LeaseToken, LeaseSeconds: leaseSeconds(lease)})
	return checkBuildLease(n, err)
}
func (s *Store) ValidateTemplateBuild(ctx context.Context, job template.BuildJob) error {
	n, err := dbsqlc.New(s.pool).ValidateTemplateBuild(ctx, dbsqlc.ValidateTemplateBuildParams{ID: job.ID, LeaseToken: job.LeaseToken})
	return checkBuildLease(n, err)
}
func (s *Store) FailTemplateBuild(ctx context.Context, job template.BuildJob, summary string) error {
	n, err := dbsqlc.New(s.pool).FailTemplateBuild(ctx, dbsqlc.FailTemplateBuildParams{ID: job.ID, LeaseToken: job.LeaseToken, Summary: summary})
	return checkBuildLease(n, err)
}
func (s *Store) CompleteTemplateBuild(ctx context.Context, job template.BuildJob, digest, bucket string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	if _, err = q.LockOwnedTemplate(ctx, dbsqlc.LockOwnedTemplateParams{ID: job.TemplateID, TenantID: job.TenantID}); err != nil {
		return templateError(err)
	}
	n, err := q.CompleteTemplateBuild(ctx, dbsqlc.CompleteTemplateBuildParams{ID: job.ID, LeaseToken: job.LeaseToken, Digest: new(digest), Bucket: new(bucket), Prefix: new("templates/sha256/" + digest)})
	if err = checkBuildLease(n, err); err != nil {
		return err
	}
	current, err := q.GetTemplateTag(ctx, dbsqlc.GetTemplateTagParams{TenantID: job.TenantID, TemplateID: job.TemplateID, Tag: job.PublishTag})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if current.BuildID != job.ExpectedTagBuildID {
		if err = q.SetBuildPublicationError(ctx, dbsqlc.SetBuildPublicationErrorParams{ID: job.ID, Message: "Tag changed during build; the Ready build was retained without moving the tag."}); err != nil {
			return err
		}
	} else if err = q.UpsertTemplateTag(ctx, dbsqlc.UpsertTemplateTagParams{TemplateID: job.TemplateID, Tag: job.PublishTag, BuildID: job.ID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
