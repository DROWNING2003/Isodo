package postgres

import (
	"context"
	"errors"
	"regexp"

	"github.com/DROWNING2003/Isodo/internal/control/postgres/dbsqlc"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var catalogName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
var manifestDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type SystemPublication struct {
	TemplateID     string `json:"template_id"`
	BuildID        string `json:"build_id"`
	ManifestDigest string `json:"manifest_digest"`
}

// RegisterSystemTemplate is an operator-only publication transaction. Callers
// must first verify Guest Ready and successfully publish the immutable S3 files.
// An empty expectedBuildID creates a catalog entry; replacement requires CAS.
func (s *Store) RegisterSystemTemplate(ctx context.Context, name, description, digest, bucket, expectedBuildID string) (SystemPublication, error) {
	var result SystemPublication
	if !catalogName.MatchString(name) || len(description) > 2048 || !manifestDigest.MatchString(digest) || bucket == "" {
		return result, errors.New("valid system template name, description, manifest digest and bucket are required")
	}
	if expectedBuildID != "" {
		if err := uuid.Validate(expectedBuildID); err != nil {
			return result, errors.New("invalid expected system build ID")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	if err = q.LockSystemTemplatePublication(ctx, name); err != nil {
		return result, err
	}
	current, err := q.GetSystemTemplatePublication(ctx, name)
	switch {
	case err == nil:
		result.TemplateID = current.CTemplateID
		// Idempotent retries do not create a second build or move any pointer.
		if current.ManifestDigest != nil && *current.ManifestDigest == digest && current.ArtifactBucket != nil && *current.ArtifactBucket == bucket {
			result.BuildID = current.CBuildID
			result.ManifestDigest = digest
			return result, tx.Commit(ctx)
		}
		if expectedBuildID != current.CBuildID {
			return result, template.ErrConflict
		}
	case errors.Is(err, pgx.ErrNoRows):
		if expectedBuildID != "" {
			return result, template.ErrConflict
		}
		result.TemplateID, err = q.CreateSystemTemplate(ctx, dbsqlc.CreateSystemTemplateParams{Name: name, Description: description})
		if err != nil {
			return result, err
		}
	default:
		return result, err
	}
	result.BuildID, err = q.CreateSystemTemplateBuild(ctx, dbsqlc.CreateSystemTemplateBuildParams{TemplateID: result.TemplateID, Digest: new(digest), Bucket: new(bucket), Prefix: new("templates/sha256/" + digest)})
	if err != nil {
		return result, err
	}
	if err = q.UpsertTemplateTag(ctx, dbsqlc.UpsertTemplateTagParams{TemplateID: result.TemplateID, Tag: "default", BuildID: result.BuildID}); err != nil {
		return result, err
	}
	if err = q.PublishSystemTemplateCatalog(ctx, dbsqlc.PublishSystemTemplateCatalogParams{Name: name, TemplateID: result.TemplateID, BuildID: result.BuildID}); err != nil {
		return result, err
	}
	result.ManifestDigest = digest
	return result, tx.Commit(ctx)
}
