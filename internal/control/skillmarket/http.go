// Package skillmarket owns immutable instruction Skills, favorites and explicit
// per-Sandbox selection. Skill content is data; no host commands or URL imports.
package skillmarket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/skillartifact"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runtime interface {
	NodeID() string
	SyncSkills(context.Context, *nodev1.SyncSkillsRequest) (*nodev1.SyncSkillsResponse, error)
}
type Handler struct {
	pool    *pgxpool.Pool
	runtime Runtime
}

func New(pool *pgxpool.Pool, runtime Runtime) *Handler { return &Handler{pool: pool, runtime: runtime} }
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if status != 204 {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func fail(w http.ResponseWriter, status int, code string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": "Skill 操作未完成，请检查内容、权限或同步状态后重试。"}})
}
func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (string, bool) {
	i, ok := auth.IdentityFromContext(r.Context())
	if !ok || i.TenantID == "" {
		fail(w, 401, "UNAUTHENTICATED")
		return "", false
	}
	if i.Credential == "api_key" {
		fail(w, 403, "SESSION_REQUIRED")
		return "", false
	}
	if h == nil || h.pool == nil {
		fail(w, 503, "SKILL_UNAVAILABLE")
		return "", false
	}
	return i.TenantID, true
}

type Entry struct {
	skillartifact.Skill
	Source   string `json:"source"`
	Favorite bool   `json:"favorite"`
}

func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.identity(w, r)
	if !ok {
		return
	}
	rows, err := h.pool.Query(r.Context(), `SELECT s.id::text,s.name,s.description,s.body,CASE WHEN s.tenant_id IS NULL THEN 'market' ELSE 'personal' END,EXISTS(SELECT 1 FROM skill_favorites f WHERE f.skill_id=s.id AND f.tenant_id=$1) FROM skill_catalog s WHERE s.tenant_id IS NULL OR s.tenant_id=$1 ORDER BY s.name,s.id LIMIT 200`, tenant)
	if err != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []Entry{}
	for rows.Next() {
		var v Entry
		if rows.Scan(&v.ID, &v.Name, &v.Description, &v.Body, &v.Source, &v.Favorite) != nil {
			fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
			return
		}
		items = append(items, v)
	}
	if rows.Err() != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	write(w, 200, map[string]any{"items": items})
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.identity(w, r)
	if !ok {
		return
	}
	var v skillartifact.Skill
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 40000))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || v.Validate() != nil || v.ID != "" {
		fail(w, 400, "SKILL_INVALID")
		return
	}
	v.ID = uuid.NewString()
	// Serialize account quota checks without a global lock.
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, tenant); err != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	var count int
	if tx.QueryRow(r.Context(), `SELECT count(*) FROM skill_catalog WHERE tenant_id=$1`, tenant).Scan(&count) != nil || count >= 100 {
		fail(w, 409, "SKILL_LIMIT")
		return
	}
	var reserved bool
	if tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM skill_catalog WHERE tenant_id IS NULL AND name=$1)`, v.Name).Scan(&reserved) != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	if reserved {
		fail(w, 409, "SKILL_NAME_CONFLICT")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO skill_catalog(id,tenant_id,name,description,body) VALUES($1,$2,$3,$4,$5)`, v.ID, tenant, v.Name, v.Description, v.Body)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO skill_favorites(tenant_id,skill_id) VALUES($1,$2)`, tenant, v.ID)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 409, "SKILL_CREATE_FAILED")
		return
	}
	write(w, 201, Entry{Skill: v, Source: "personal", Favorite: true})
}
func (h *Handler) Favorite(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.identity(w, r)
	if !ok {
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		fail(w, 404, "SKILL_NOT_FOUND")
		return
	}
	if r.Method == "DELETE" {
		if _, err := h.pool.Exec(r.Context(), `DELETE FROM skill_favorites WHERE tenant_id=$1 AND skill_id=$2`, tenant, id); err != nil {
			fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
			return
		}
		write(w, 204, nil)
		return
	}
	tag, err := h.pool.Exec(r.Context(), `INSERT INTO skill_favorites(tenant_id,skill_id) SELECT $1,id FROM skill_catalog WHERE id=$2 AND (tenant_id IS NULL OR tenant_id=$1) ON CONFLICT DO NOTHING`, tenant, id)
	if err != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	if tag.RowsAffected() == 0 {
		var visible bool
		_ = h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM skill_catalog WHERE id=$2 AND (tenant_id IS NULL OR tenant_id=$1))`, tenant, id).Scan(&visible)
		if !visible {
			fail(w, 404, "SKILL_NOT_FOUND")
			return
		}
	}
	write(w, 204, nil)
}

type Selection struct {
	SkillIDs        []string `json:"skill_ids"`
	Version         int64    `json:"version"`
	AppliedVersion  int64    `json:"applied_version"`
	SyncStatus      string   `json:"sync_status"`
	RestartRequired bool     `json:"restart_required"`
}

func (h *Handler) Sandbox(w http.ResponseWriter, r *http.Request, id string) {
	tenant, ok := h.identity(w, r)
	if !ok {
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		fail(w, 404, "SANDBOX_NOT_FOUND")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	var node, desired, observed string
	var generation, revision uint64
	err = tx.QueryRow(ctx, `SELECT COALESCE(node_id,''),desired_state,observed_state,ownership_generation,revision FROM sandboxes WHERE id=$1 AND tenant_id=$2 AND desired_state!='deleted' AND deleted_at IS NULL AND deletion_requested_at IS NULL FOR UPDATE`, id, tenant).Scan(&node, &desired, &observed, &generation, &revision)
	if err != nil {
		fail(w, 404, "SANDBOX_NOT_FOUND")
		return
	}
	v := Selection{SkillIDs: []string{}, SyncStatus: "pending"}
	err = tx.QueryRow(ctx, `SELECT skill_ids::text[],version,applied_version,sync_status FROM sandbox_skills WHERE sandbox_id=$1 AND tenant_id=$2`, id, tenant).Scan(&v.SkillIDs, &v.Version, &v.AppliedVersion, &v.SyncStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	if r.Method == "GET" {
		write(w, 200, v)
		return
	}
	var input struct {
		SkillIDs []string `json:"skill_ids"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF || input.SkillIDs == nil || len(input.SkillIDs) > skillartifact.MaxSkills {
		fail(w, 400, "SKILL_INVALID_SELECTION")
		return
	}
	seen := map[string]bool{}
	documents := []*nodev1.SkillDocument{}
	for index, skillID := range input.SkillIDs {
		parsed, parseErr := uuid.Parse(skillID)
		skillID = parsed.String()
		input.SkillIDs[index] = skillID
		if parseErr != nil || seen[skillID] {
			fail(w, 400, "SKILL_INVALID_SELECTION")
			return
		}
		seen[skillID] = true
		var document nodev1.SkillDocument
		document.Id = skillID
		// Retained bindings remain valid after unfavoriting; additions require a favorite.
		err = tx.QueryRow(ctx, `SELECT s.name,s.description,s.body FROM skill_catalog s WHERE s.id=$1 AND (s.tenant_id IS NULL OR s.tenant_id=$2) AND (EXISTS(SELECT 1 FROM skill_favorites WHERE tenant_id=$2 AND skill_id=s.id) OR s.id=ANY($3::uuid[]))`, skillID, tenant, v.SkillIDs).Scan(&document.Name, &document.Description, &document.Body)
		if err != nil {
			fail(w, 409, "SKILL_FAVORITE_REQUIRED")
			return
		}
		documents = append(documents, &document)
	}
	artifacts := make([]skillartifact.Skill, 0, len(documents))
	for _, d := range documents {
		artifacts = append(artifacts, skillartifact.Skill{ID: d.Id, Name: d.Name, Description: d.Description, Body: d.Body})
	}
	_, encodeErr := skillartifact.Encode(artifacts)
	if encodeErr != nil {
		fail(w, 400, "SKILL_SELECTION_TOO_LARGE")
		return
	}
	v.SkillIDs = input.SkillIDs
	if v.SkillIDs == nil {
		v.SkillIDs = []string{}
	}
	v.Version++
	v.SyncStatus = "pending"
	v.RestartRequired = true
	_, err = tx.Exec(ctx, `INSERT INTO sandbox_skills(sandbox_id,tenant_id,skill_ids,version) VALUES($1,$2,$3,$4) ON CONFLICT(sandbox_id) DO UPDATE SET skill_ids=EXCLUDED.skill_ids,version=EXCLUDED.version,sync_status='pending'`, id, tenant, v.SkillIDs, v.Version)
	if err != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	code := 202
	if h.runtime != nil && node == h.runtime.NodeID() && desired == "running" && observed == "running" {
		_, syncErr := h.runtime.SyncSkills(ctx, &nodev1.SyncSkillsRequest{Target: &nodev1.SandboxTarget{SandboxId: id, OwnershipGeneration: generation, Revision: revision}, Skills: documents, Version: uint64(v.Version)})
		if syncErr == nil {
			v.SyncStatus = "applied"
			v.AppliedVersion = v.Version
			code = 200
		} else {
			v.SyncStatus = "failed"
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox_skills SET applied_version=$2,sync_status=$3 WHERE sandbox_id=$1`, id, v.AppliedVersion, v.SyncStatus); err != nil || tx.Commit(ctx) != nil {
		fail(w, 503, "SKILL_STORAGE_UNAVAILABLE")
		return
	}
	write(w, code, v)
}
