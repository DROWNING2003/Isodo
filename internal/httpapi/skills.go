package httpapi

import (
	"github.com/DROWNING2003/Isodo/internal/control/skillmarket"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
)

type SkillHandlers struct{ Market *skillmarket.Handler }

func (h *SkillHandlers) available(w http.ResponseWriter) bool {
	if h == nil || h.Market == nil {
		writeError(w, 503, "SKILL_UNAVAILABLE", "Skill market is not configured.")
		return false
	}
	return true
}
func (h *SkillHandlers) ListSkills(w http.ResponseWriter, r *http.Request) {
	if h.available(w) {
		h.Market.Catalog(w, r)
	}
}
func (h *SkillHandlers) CreateSkill(w http.ResponseWriter, r *http.Request) {
	if h.available(w) {
		h.Market.Create(w, r)
	}
}
func (h *SkillHandlers) FavoriteSkill(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if h.available(w) {
		h.Market.Favorite(w, r, id.String())
	}
}
func (h *SkillHandlers) UnfavoriteSkill(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if h.available(w) {
		h.Market.Favorite(w, r, id.String())
	}
}
func (h *SkillHandlers) GetSandboxSkills(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if h.available(w) {
		h.Market.Sandbox(w, r, id.String())
	}
}
func (h *SkillHandlers) SetSandboxSkills(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if h.available(w) {
		h.Market.Sandbox(w, r, id.String())
	}
}
