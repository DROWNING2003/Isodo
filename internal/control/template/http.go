// Package template implements the personal template registry and build API.
package template

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("template not found")
	ErrConflict = errors.New("template state conflict")
	tagPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
)

type Store interface {
	CreateTemplate(context.Context, string, api.CreateTemplate) (api.Template, error)
	ListTemplates(context.Context, string, string, int, int) ([]api.Template, int64, error)
	GetTemplate(context.Context, string, string) (api.Template, error)
	UpdateTemplate(context.Context, string, string, api.PatchTemplate) (api.Template, error)
	DeleteTemplate(context.Context, string, string) error
	CreateTemplateBuild(context.Context, string, string, api.BuildRecipe) (api.TemplateBuild, error)
	GetTemplateBuild(context.Context, string, string, string) (api.TemplateBuild, error)
	GetTemplateTag(context.Context, string, string, string) (api.TemplateTag, error)
	SetTemplateTag(context.Context, string, string, string, api.SetTemplateTag) (api.TemplateTag, error)
	DeleteTemplateTag(context.Context, string, string, string) error
	ForkTemplate(context.Context, string, string, api.ForkTemplate) (api.Template, error)
}

type Handler struct{ store Store }

func NewHandler(store Store) *Handler { return &Handler{store: store} }

func (h *Handler) tenant(w http.ResponseWriter, r *http.Request, ids ...string) (string, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		fail(w, 401, "UNAUTHENTICATED", "Authentication is required.")
		return "", false
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			result(w, nil, ErrNotFound, 200)
			return "", false
		}
	}
	if h.store == nil {
		fail(w, 503, "TEMPLATES_UNAVAILABLE", "Template service is not configured.")
		return "", false
	}
	return identity.TenantID, true
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "INVALID_JSON", "Supply a JSON object with known fields.")
		return false
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		fail(w, 400, "INVALID_JSON", "Supply exactly one JSON object.")
		return false
	}
	return true
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func result(w http.ResponseWriter, v any, err error, status int) {
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, 404, "TEMPLATE_NOT_FOUND", "Template or build was not found.")
	case errors.Is(err, ErrConflict):
		fail(w, 409, "TEMPLATE_CONFLICT", "The template or build state changed; reload and retry.")
	case err != nil:
		fail(w, 500, "INTERNAL", "The template operation could not be completed.")
	default:
		write(w, status, v)
	}
}
func validFields(name string, description *string) bool {
	return len(name) > 0 && len(name) <= 128 && (description == nil || len(*description) <= 2048)
}
func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var v api.CreateTemplate
	if !decode(w, r, &v) {
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	if !validFields(v.Name, v.Description) || (v.Visibility != nil && !v.Visibility.Valid()) {
		fail(w, 400, "INVALID_TEMPLATE", "Check name, description and visibility.")
		return
	}
	item, err := h.store.CreateTemplate(r.Context(), tenant, v)
	result(w, item, err, 201)
}
func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	item, err := h.store.GetTemplate(r.Context(), tenant, id)
	result(w, item, err, 200)
}
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	var v api.PatchTemplate
	if !decode(w, r, &v) {
		return
	}
	if v.Name == nil && v.Description == nil && v.Visibility == nil {
		fail(w, 400, "EMPTY_PATCH", "Supply at least one mutable field.")
		return
	}
	if v.Name != nil {
		n := strings.TrimSpace(*v.Name)
		v.Name = &n
		if !validFields(n, nil) {
			fail(w, 400, "INVALID_TEMPLATE", "Check template name.")
			return
		}
	}
	if (v.Description != nil && len(*v.Description) > 2048) || (v.Visibility != nil && !v.Visibility.Valid()) {
		fail(w, 400, "INVALID_TEMPLATE", "Check description and visibility.")
		return
	}
	item, err := h.store.UpdateTemplate(r.Context(), tenant, id, v)
	result(w, item, err, 200)
}
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	result(w, nil, h.store.DeleteTemplate(r.Context(), tenant, id), 204)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request, scope string, limitPtr, offsetPtr *int) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	limit, offset := 50, 0
	if limitPtr != nil {
		limit = *limitPtr
	}
	if offsetPtr != nil {
		offset = *offsetPtr
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		fail(w, 400, "INVALID_PAGINATION", "limit must be 1..100; offset must be 0..10000.")
		return
	}
	items, total, err := h.store.ListTemplates(r.Context(), tenant, scope, limit, offset)
	result(w, api.TemplateList{Items: items, Total: total, Limit: limit, Offset: offset}, err, 200)
}
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request, p api.ListTemplatesParams) {
	scope := "owned"
	if p.Scope != nil {
		if !p.Scope.Valid() {
			fail(w, 400, "INVALID_SCOPE", "Invalid template list scope.")
			return
		}
		scope = string(*p.Scope)
	}
	h.list(w, r, scope, p.Limit, p.Offset)
}
func (h *Handler) ListSystemTemplates(w http.ResponseWriter, r *http.Request, p api.ListSystemTemplatesParams) {
	h.list(w, r, "system", p.Limit, p.Offset)
}
func (h *Handler) CreateTemplateBuild(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	var v api.BuildRecipe
	if !decode(w, r, &v) {
		return
	}
	if _, err := uuid.Parse(v.SourceBuildID); err != nil {
		fail(w, 400, "INVALID_RECIPE", "Supply a source build ID.")
		return
	}
	if v.PublishTag == nil {
		v.PublishTag = new("default")
	}
	if !tagPattern.MatchString(*v.PublishTag) {
		fail(w, 400, "INVALID_TAG", "Invalid publication tag.")
		return
	}
	if v.Commands != nil {
		if len(*v.Commands) > 32 {
			fail(w, 400, "INVALID_RECIPE", "At most 32 commands are allowed.")
			return
		}
		for _, argv := range *v.Commands {
			if len(argv) == 0 || len(argv) > 128 || argv[0] == "" {
				fail(w, 400, "INVALID_RECIPE", "Each command requires argv.")
				return
			}
			for _, arg := range argv {
				if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
					fail(w, 400, "INVALID_RECIPE", "Invalid command argument.")
					return
				}
			}
		}
	}
	item, err := h.store.CreateTemplateBuild(r.Context(), tenant, id, v)
	result(w, item, err, 202)
}
func (h *Handler) GetTemplateBuild(w http.ResponseWriter, r *http.Request, id, build string) {
	tenant, ok := h.tenant(w, r, id, build)
	if !ok {
		return
	}
	item, err := h.store.GetTemplateBuild(r.Context(), tenant, id, build)
	result(w, item, err, 200)
}
func (h *Handler) GetTemplateTag(w http.ResponseWriter, r *http.Request, id, tag string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	if !tagPattern.MatchString(tag) {
		result(w, nil, ErrNotFound, 200)
		return
	}
	item, err := h.store.GetTemplateTag(r.Context(), tenant, id, tag)
	result(w, item, err, 200)
}
func (h *Handler) SetTemplateTag(w http.ResponseWriter, r *http.Request, id, tag string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	var v api.SetTemplateTag
	if !decode(w, r, &v) {
		return
	}
	_, err := uuid.Parse(v.BuildID)
	if err != nil || !tagPattern.MatchString(tag) {
		fail(w, 400, "INVALID_TAG", "Supply a valid tag and build ID.")
		return
	}
	if v.ExpectedBuildID != nil && *v.ExpectedBuildID != "" {
		if _, err := uuid.Parse(*v.ExpectedBuildID); err != nil {
			fail(w, 400, "INVALID_TAG", "Invalid expected build ID.")
			return
		}
	}
	item, err := h.store.SetTemplateTag(r.Context(), tenant, id, tag, v)
	result(w, item, err, 200)
}
func (h *Handler) DeleteTemplateTag(w http.ResponseWriter, r *http.Request, id, tag string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	if !tagPattern.MatchString(tag) {
		result(w, nil, ErrNotFound, 204)
		return
	}
	result(w, nil, h.store.DeleteTemplateTag(r.Context(), tenant, id, tag), 204)
}
func (h *Handler) ForkTemplate(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.tenant(w, r, id)
	if !ok {
		return
	}
	var v api.ForkTemplate
	if !decode(w, r, &v) {
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	if _, err := uuid.Parse(v.BuildID); err != nil || !validFields(v.Name, nil) || (v.Visibility != nil && !v.Visibility.Valid()) {
		fail(w, 400, "INVALID_TEMPLATE", "Check source build, name and visibility.")
		return
	}
	item, err := h.store.ForkTemplate(r.Context(), tenant, id, v)
	result(w, item, err, 201)
}
