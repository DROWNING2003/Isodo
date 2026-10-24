package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/postgres/dbsqlc"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/jackc/pgx/v5"
)

var _ template.Store = (*Store)(nil)

func templateError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return template.ErrNotFound
	}
	return err
}
func templateValue(id, name, description, scope, visibility string, created, updated time.Time, owned bool) api.Template {
	return api.Template{ID: id, Name: name, Description: description, Scope: api.TemplateScope(scope), Visibility: api.TemplateVisibility(visibility), CreatedAt: created, UpdatedAt: updated, Owned: owned}
}
func templateCreated(r dbsqlc.CreateTemplateRow) api.Template {
	return templateValue(r.ID, r.Name, r.Description, r.Scope, r.Visibility, r.CreatedAt.Time, r.UpdatedAt.Time, true)
}
func templateRead(r dbsqlc.GetTemplateRow) api.Template {
	return templateValue(r.ID, r.Name, r.Description, r.Scope, r.Visibility, r.CreatedAt.Time, r.UpdatedAt.Time, r.Owned)
}
func templateCreateParams(tenant string, v api.CreateTemplate) dbsqlc.CreateTemplateParams {
	description, visibility := "", "private"
	if v.Description != nil {
		description = *v.Description
	}
	if v.Visibility != nil {
		visibility = string(*v.Visibility)
	}
	return dbsqlc.CreateTemplateParams{TenantID: tenant, Name: v.Name, Description: description, Visibility: visibility}
}
func (s *Store) CreateTemplate(ctx context.Context, tenant string, v api.CreateTemplate) (api.Template, error) {
	row, err := dbsqlc.New(s.pool).CreateTemplate(ctx, templateCreateParams(tenant, v))
	return templateCreated(row), templateError(err)
}
func (s *Store) GetTemplate(ctx context.Context, tenant, id string) (api.Template, error) {
	row, err := dbsqlc.New(s.pool).GetTemplate(ctx, dbsqlc.GetTemplateParams{TenantID: tenant, ID: id})
	return templateRead(row), templateError(err)
}
func (s *Store) ListTemplates(ctx context.Context, tenant, scope string, limit, offset int) ([]api.Template, int64, error) {
	// One snapshot keeps totals consistent with items during concurrent publication/deletion.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	total, err := q.CountTemplates(ctx, dbsqlc.CountTemplatesParams{TenantID: tenant, ListScope: scope})
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.ListTemplates(ctx, dbsqlc.ListTemplatesParams{TenantID: tenant, ListScope: scope, PageLimit: int32(limit), PageOffset: int32(offset)})
	if err != nil {
		return nil, 0, err
	}
	items := make([]api.Template, 0, len(rows))
	for _, r := range rows {
		items = append(items, templateRead(dbsqlc.GetTemplateRow(r)))
	}
	return items, total, tx.Commit(ctx)
}
func (s *Store) UpdateTemplate(ctx context.Context, tenant, id string, v api.PatchTemplate) (api.Template, error) {
	var visibility *string
	if v.Visibility != nil {
		visibility = new(string(*v.Visibility))
	}
	row, err := dbsqlc.New(s.pool).UpdateTemplate(ctx, dbsqlc.UpdateTemplateParams{ID: id, TenantID: tenant, Name: v.Name, Description: v.Description, Visibility: visibility})
	return templateCreated(dbsqlc.CreateTemplateRow(row)), templateError(err)
}
func (s *Store) DeleteTemplate(ctx context.Context, tenant, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	if _, err = q.LockOwnedTemplate(ctx, dbsqlc.LockOwnedTemplateParams{ID: id, TenantID: tenant}); err != nil {
		return templateError(err)
	}
	active, err := q.HasActiveTemplateBuild(ctx, id)
	if err != nil {
		return err
	}
	if active {
		return template.ErrConflict
	}
	if err = q.FailQueuedTemplateBuilds(ctx, id); err != nil {
		return err
	}
	if err = q.SoftDeleteTemplate(ctx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func buildCreated(r dbsqlc.CreateTemplateBuildRow) api.TemplateBuild {
	v := api.TemplateBuild{ID: r.ID, TemplateID: r.TemplateID, OperationID: r.OperationID, SourceBuildID: r.SourceBuildID, Status: api.TemplateBuildStatus(r.Status), ManifestDigest: r.ManifestDigest, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if r.PublicationError != "" {
		v.PublicationError = new(r.PublicationError)
	}
	if r.ErrorSummary != "" {
		v.Error = new(r.ErrorSummary)
	}
	return v
}
func (s *Store) CreateTemplateBuild(ctx context.Context, tenant, id string, recipe api.BuildRecipe) (api.TemplateBuild, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.TemplateBuild{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	if _, err = q.LockOwnedTemplate(ctx, dbsqlc.LockOwnedTemplateParams{ID: id, TenantID: tenant}); err != nil {
		return api.TemplateBuild{}, templateError(err)
	}
	if _, err = q.GetAccessibleSourceBuild(ctx, dbsqlc.GetAccessibleSourceBuildParams{TenantID: tenant, BuildID: recipe.SourceBuildID}); err != nil {
		return api.TemplateBuild{}, templateError(err)
	}
	data, err := json.Marshal(recipe)
	if err != nil {
		return api.TemplateBuild{}, err
	}
	tag := "default"
	if recipe.PublishTag != nil {
		tag = *recipe.PublishTag
	}
	previous, tagErr := q.GetTemplateTag(ctx, dbsqlc.GetTemplateTagParams{TenantID: tenant, TemplateID: id, Tag: tag})
	if tagErr != nil && !errors.Is(tagErr, pgx.ErrNoRows) {
		return api.TemplateBuild{}, tagErr
	}
	var expected *string
	if previous.BuildID != "" {
		expected = &previous.BuildID
	}
	row, err := q.CreateTemplateBuild(ctx, dbsqlc.CreateTemplateBuildParams{TemplateID: id, SourceBuildID: recipe.SourceBuildID, Recipe: data, PublishTag: tag, ExpectedTagBuildID: expected})
	if err != nil {
		return api.TemplateBuild{}, err
	}
	return buildCreated(row), tx.Commit(ctx)
}
func (s *Store) GetTemplateBuild(ctx context.Context, tenant, id, build string) (api.TemplateBuild, error) {
	r, err := dbsqlc.New(s.pool).GetAccessibleTemplateBuild(ctx, dbsqlc.GetAccessibleTemplateBuildParams{TenantID: tenant, TemplateID: id, BuildID: build})
	if err != nil {
		return api.TemplateBuild{}, templateError(err)
	}
	value := buildCreated(dbsqlc.CreateTemplateBuildRow{ID: r.ID, TemplateID: r.TemplateID, OperationID: r.OperationID, SourceBuildID: r.SourceBuildID, Status: r.Status, ManifestDigest: r.ManifestDigest, ErrorSummary: r.ErrorSummary, PublicationError: r.PublicationError, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	if !r.Owned {
		value.Error = nil
		value.PublicationError = nil
		value.SourceBuildID = ""
	}
	return value, nil
}
func (s *Store) GetTemplateTag(ctx context.Context, tenant, id, tag string) (api.TemplateTag, error) {
	row, err := dbsqlc.New(s.pool).GetTemplateTag(ctx, dbsqlc.GetTemplateTagParams{TenantID: tenant, TemplateID: id, Tag: tag})
	return api.TemplateTag{Tag: row.Name, BuildID: row.BuildID}, templateError(err)
}
func (s *Store) SetTemplateTag(ctx context.Context, tenant, id, tag string, v api.SetTemplateTag) (api.TemplateTag, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.TemplateTag{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	if _, err = q.LockOwnedTemplate(ctx, dbsqlc.LockOwnedTemplateParams{ID: id, TenantID: tenant}); err != nil {
		return api.TemplateTag{}, templateError(err)
	}
	if _, err = q.GetReadyBuild(ctx, dbsqlc.GetReadyBuildParams{TemplateID: id, BuildID: v.BuildID}); err != nil {
		return api.TemplateTag{}, templateError(err)
	}
	if v.ExpectedBuildID != nil {
		old, err := q.GetTemplateTag(ctx, dbsqlc.GetTemplateTagParams{TenantID: tenant, TemplateID: id, Tag: tag})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return api.TemplateTag{}, err
		}
		if old.BuildID != *v.ExpectedBuildID {
			return api.TemplateTag{}, template.ErrConflict
		}
	}
	if err = q.UpsertTemplateTag(ctx, dbsqlc.UpsertTemplateTagParams{TemplateID: id, Tag: tag, BuildID: v.BuildID}); err != nil {
		return api.TemplateTag{}, err
	}
	return api.TemplateTag{Tag: tag, BuildID: v.BuildID}, tx.Commit(ctx)
}
func (s *Store) DeleteTemplateTag(ctx context.Context, tenant, id, tag string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	if _, err = q.LockOwnedTemplate(ctx, dbsqlc.LockOwnedTemplateParams{ID: id, TenantID: tenant}); err != nil {
		return templateError(err)
	}
	if err = q.DeleteTemplateTag(ctx, dbsqlc.DeleteTemplateTagParams{TemplateID: id, Tag: tag}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ForkTemplate(ctx context.Context, tenant, id string, v api.ForkTemplate) (api.Template, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.Template{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := dbsqlc.New(tx)
	// Lock source ownership/visibility for the entire fork; revocation cannot race this decision.
	if _, err = q.GetAccessibleSourceBuild(ctx, dbsqlc.GetAccessibleSourceBuildParams{TenantID: tenant, BuildID: v.BuildID}); err != nil {
		return api.Template{}, templateError(err)
	}
	source, err := q.GetAccessibleTemplateBuild(ctx, dbsqlc.GetAccessibleTemplateBuildParams{TenantID: tenant, TemplateID: id, BuildID: v.BuildID})
	if err != nil {
		return api.Template{}, templateError(err)
	}
	if source.Status != "ready" {
		return api.Template{}, template.ErrConflict
	}
	visibility := api.CreateTemplateVisibilityPrivate
	if v.Visibility != nil {
		visibility = api.CreateTemplateVisibility(*v.Visibility)
	}
	row, err := q.CreateTemplate(ctx, templateCreateParams(tenant, api.CreateTemplate{Name: v.Name, Visibility: &visibility}))
	if err != nil {
		return api.Template{}, err
	}
	build, err := q.CloneReadyTemplateBuild(ctx, dbsqlc.CloneReadyTemplateBuildParams{TemplateID: row.ID, SourceBuildID: v.BuildID})
	if err != nil {
		return api.Template{}, err
	}
	if err = q.UpsertTemplateTag(ctx, dbsqlc.UpsertTemplateTagParams{TemplateID: row.ID, Tag: "default", BuildID: build}); err != nil {
		return api.Template{}, err
	}
	return templateCreated(row), tx.Commit(ctx)
}
