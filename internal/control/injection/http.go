package injection

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/google/uuid"
)

// Handler serves the account-level injection rule API.
type Handler struct {
	store  Store
	sealer *Sealer
}

func NewHandler(store Store, sealer *Sealer) *Handler {
	return &Handler{store: store, sealer: sealer}
}

func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		fail(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return auth.Identity{}, false
	}
	// A scoped sandbox API key must never be able to read or rewrite account
	// credentials. Only an interactive session or a bearer login can.
	if identity.Credential == "api_key" {
		fail(w, http.StatusForbidden, "SESSION_REQUIRED", "Secure connections require an interactive Isodo session.")
		return auth.Identity{}, false
	}
	if h.store == nil || h.sealer == nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "Secure connections are not configured.")
		return auth.Identity{}, false
	}
	return identity, true
}

func (h *Handler) ListInjectionRules(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	rules, err := h.store.List(r.Context(), identity.TenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "Secure connections could not be loaded.")
		return
	}
	items := make([]api.InjectionRule, 0, len(rules))
	for _, rule := range rules {
		items = append(items, rule.View())
	}
	write(w, http.StatusOK, api.InjectionRuleList{Items: items, Total: int64(len(items))})
}

func (h *Handler) CreateInjectionRule(w http.ResponseWriter, r *http.Request) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	var input api.CreateInjectionRule
	if !decode(w, r, &input) {
		return
	}
	baseURL, headerName := "", ""
	if input.BaseUrl != nil {
		baseURL = *input.BaseUrl
	}
	if input.HeaderName != nil {
		headerName = *input.HeaderName
	}
	rule, err := Validate(input.Name, string(input.Type), baseURL, headerName, derefMap(input.IfHeaders), derefMap(input.IfQueries), input.Secret)
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_INJECTION_RULE", err.Error())
		return
	}
	rule.TenantID = identity.TenantID
	if err := h.sealRule(&rule, input.Secret); err != nil {
		fail(w, http.StatusInternalServerError, "INJECTION_SEAL_FAILED", "The secret could not be stored.")
		return
	}
	created, err := h.store.Create(r.Context(), rule)
	if errors.Is(err, ErrExists) {
		fail(w, http.StatusConflict, "INJECTION_RULE_EXISTS", "A connection with this name already exists.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "The secure connection could not be created.")
		return
	}
	write(w, http.StatusCreated, created.View())
}

func (h *Handler) GetInjectionRule(w http.ResponseWriter, r *http.Request, ruleID string) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	if !validID(ruleID) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	rule, err := h.store.Get(r.Context(), identity.TenantID, ruleID)
	if errors.Is(err, ErrNotFound) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "The secure connection could not be loaded.")
		return
	}
	write(w, http.StatusOK, rule.View())
}

func (h *Handler) UpdateInjectionRule(w http.ResponseWriter, r *http.Request, ruleID string) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	if !validID(ruleID) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	var input api.PatchInjectionRule
	if !decode(w, r, &input) {
		return
	}
	existing, err := h.store.Get(r.Context(), identity.TenantID, ruleID)
	if errors.Is(err, ErrNotFound) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "The secure connection could not be loaded.")
		return
	}
	name, baseURL, headerName := existing.Name, existing.BaseURL, existing.HeaderName
	if input.Name != nil {
		name = *input.Name
	}
	if input.BaseUrl != nil {
		baseURL = *input.BaseUrl
	}
	if input.HeaderName != nil {
		headerName = *input.HeaderName
	}
	ifHeaders, ifQueries := existing.IfHeaders, existing.IfQueries
	if input.IfHeaders != nil {
		ifHeaders = *input.IfHeaders
	}
	if input.IfQueries != nil {
		ifQueries = *input.IfQueries
	}
	// Validate the non-secret shape first; an omitted secret keeps the sealed
	// value and does not need to be re-read or re-encrypted.
	secret := ""
	if input.Secret != nil {
		secret = strings.TrimSpace(*input.Secret)
		if secret == "" {
			fail(w, http.StatusBadRequest, "INVALID_INJECTION_RULE", "A supplied secret must not be blank.")
			return
		}
	}
	validated, err := Validate(name, existing.Type, baseURL, headerName, ifHeaders, ifQueries, "placeholder")
	if err != nil {
		fail(w, http.StatusBadRequest, "INVALID_INJECTION_RULE", err.Error())
		return
	}
	if secret == "" && validated.HeaderName != existing.HeaderName {
		fail(w, http.StatusBadRequest, "INVALID_INJECTION_RULE", "Changing the header requires a new secret.")
		return
	}
	validated.ID = existing.ID
	validated.TenantID = existing.TenantID
	validated.Type = existing.Type
	validated.Nonce = existing.Nonce
	validated.Sealed = existing.Sealed
	validated.SecretHint = existing.SecretHint
	if secret != "" {
		if err := h.sealRule(&validated, secret); err != nil {
			fail(w, http.StatusInternalServerError, "INJECTION_SEAL_FAILED", "The secret could not be stored.")
			return
		}
	}
	updated, err := h.store.Update(r.Context(), validated)
	if errors.Is(err, ErrExists) {
		fail(w, http.StatusConflict, "INJECTION_RULE_EXISTS", "A connection with this name already exists.")
		return
	}
	if errors.Is(err, ErrNotFound) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "The secure connection could not be updated.")
		return
	}
	write(w, http.StatusOK, updated.View())
}

func (h *Handler) DeleteInjectionRule(w http.ResponseWriter, r *http.Request, ruleID string) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	if !validID(ruleID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := h.store.Delete(r.Context(), identity.TenantID, ruleID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "The secure connection could not be deleted.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetInjectionRuleMaterial(w http.ResponseWriter, r *http.Request, ruleID string) {
	identity, ok := h.identity(w, r)
	if !ok {
		return
	}
	if !validID(ruleID) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	rule, err := h.store.Get(r.Context(), identity.TenantID, ruleID)
	if errors.Is(err, ErrNotFound) {
		fail(w, http.StatusNotFound, "INJECTION_RULE_NOT_FOUND", "Secure connection was not found.")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "INJECTIONS_UNAVAILABLE", "The secure connection could not be loaded.")
		return
	}
	plaintext, err := h.sealer.Open(rule.Nonce, rule.Sealed)
	if err != nil {
		fail(w, http.StatusInternalServerError, "INJECTION_OPEN_FAILED", "The secure connection material could not be read.")
		return
	}
	var material Material
	if err := json.Unmarshal(plaintext, &material); err != nil {
		fail(w, http.StatusInternalServerError, "INJECTION_OPEN_FAILED", "The secure connection material is invalid.")
		return
	}
	response := api.InjectionRuleMaterial{
		Id:   rule.ID,
		Type: api.InjectionRuleType(rule.Type),
	}
	if rule.BaseURL != "" {
		response.BaseUrl = &rule.BaseURL
	}
	if rule.HeaderName != "" {
		response.HeaderName = &rule.HeaderName
	}
	if len(rule.IfHeaders) > 0 {
		response.IfHeaders = &rule.IfHeaders
	}
	if len(rule.IfQueries) > 0 {
		response.IfQueries = &rule.IfQueries
	}
	if material.APIKey != "" {
		response.ApiKey = &material.APIKey
	}
	if material.Token != "" {
		response.Token = &material.Token
	}
	if len(material.Headers) > 0 {
		response.Headers = &material.Headers
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	write(w, http.StatusOK, response)
}

func (h *Handler) sealRule(rule *Rule, secret string) error {
	material := BuildMaterial(rule.Type, rule.HeaderName, secret)
	plaintext, err := json.Marshal(material)
	if err != nil {
		return err
	}
	nonce, sealed, err := h.sealer.Seal(plaintext)
	if err != nil {
		return err
	}
	rule.Nonce = nonce
	rule.Sealed = sealed
	rule.SecretHint = Hint(secret)
	return nil
}

func validID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(w, http.StatusBadRequest, "INVALID_JSON", "Supply a JSON object with known fields.")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "INVALID_JSON", "Supply exactly one JSON object.")
		return false
	}
	return true
}

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func derefMap(value *map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	return *value
}
