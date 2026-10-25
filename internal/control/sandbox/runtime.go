package sandbox

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RuntimeClient interface {
	NodeID() string
	GetInjections(context.Context, *nodev1.SandboxTarget) (*nodev1.SandboxInjections, error)
	SetInjections(context.Context, *nodev1.SetInjectionsRequest) (*nodev1.SandboxInjections, error)
}

func (h *Handler) SetRuntime(client RuntimeClient) { h.runtime = client }
func (h *Handler) runtimeTarget(w http.ResponseWriter, r *http.Request, id string) (*nodev1.SandboxTarget, bool) {
	return h.resolveRuntimeTarget(w, r, id, true)
}
func (h *Handler) resolveRuntimeTarget(w http.ResponseWriter, r *http.Request, id string, credentials bool) (*nodev1.SandboxTarget, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, 401, "UNAUTHENTICATED", "Authentication is required.")
		return nil, false
	}
	if credentials && identity.Credential == "api_key" {
		writeError(w, 403, "SESSION_REQUIRED", "Credentials require an interactive session.")
		return nil, false
	}
	if !sandboxIDPattern.MatchString(id) {
		writeSandboxNotFound(w)
		return nil, false
	}
	item, err := h.store.GetSandbox(r.Context(), identity.TenantID, id)
	if errors.Is(err, ErrNotFound) {
		writeSandboxNotFound(w)
		return nil, false
	}
	if err != nil {
		writeError(w, 503, "SANDBOX_UNAVAILABLE", "The sandbox could not be loaded.")
		return nil, false
	}
	if item.Deletion != nil {
		writeError(w, 409, "SANDBOX_DELETING", "The sandbox is being deleted.")
		return nil, false
	}
	if h.runtime == nil {
		writeError(w, 503, "SANDBOX_RUNTIME_UNAVAILABLE", "Sandbox runtime is not configured.")
		return nil, false
	}
	revision, err := strconv.ParseUint(item.Revision, 10, 64)
	if err != nil || revision == 0 || item.OwnershipGeneration == 0 || item.NodeID == "" || item.NodeID != h.runtime.NodeID() {
		writeError(w, 409, "SANDBOX_NOT_ASSIGNED", "The sandbox does not have an available Node assignment.")
		return nil, false
	}
	return &nodev1.SandboxTarget{SandboxId: id, OwnershipGeneration: item.OwnershipGeneration, Revision: revision}, true
}
func runtimeError(w http.ResponseWriter, err error) {
	code, message, httpStatus := "SANDBOX_RUNTIME_UNAVAILABLE", "Sandbox runtime could not apply this request.", 503
	switch status.Code(err) {
	case codes.InvalidArgument:
		code, httpStatus = "INVALID_INJECTIONS", 400
	case codes.NotFound:
		code, httpStatus = "SANDBOX_NOT_FOUND", 404
	case codes.FailedPrecondition, codes.Aborted:
		code, httpStatus = "SANDBOX_STATE_CONFLICT", 409
	case codes.Unimplemented:
		code, httpStatus = "SANDBOX_RUNTIME_UNSUPPORTED", 501
	}
	writeError(w, httpStatus, code, message)
}
func (h *Handler) GetSandboxInjections(w http.ResponseWriter, r *http.Request, id string) {
	target, ok := h.runtimeTarget(w, r, id)
	if !ok {
		return
	}
	result, err := h.runtime.GetInjections(r.Context(), target)
	if err != nil {
		runtimeError(w, err)
		return
	}
	writeRuntimeInjections(w, result)
}
func (h *Handler) SetSandboxInjections(w http.ResponseWriter, r *http.Request, id string) {
	target, ok := h.runtimeTarget(w, r, id)
	if !ok {
		return
	}
	var input api.SetSandboxInjectionsJSONRequestBody
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Rules == nil || len(input.Rules) > 32 {
		writeError(w, 400, "INVALID_INJECTIONS", "Supply an array of at most 32 rules.")
		return
	}
	request := &nodev1.SetInjectionsRequest{Target: target}
	for _, rule := range input.Rules {
		spec := rule.Spec
		kind := string(spec.Type)
		if kind == "mcp" {
			kind = "http"
		}
		material := &nodev1.InjectionMaterial{Key: rule.Key, Name: rule.Name, Kind: string(rule.Kind), PersistentId: value(rule.PersistentId), Spec: &nodev1.InjectionSpec{Type: kind, BaseUrl: value(spec.BaseUrl), HeaderName: value(spec.HeaderName), IfHeaders: mapValue(spec.IfHeaders), IfQueries: mapValue(spec.IfQueries)}}
		if secret := rule.Secret; secret != nil {
			material.Secret = &nodev1.InjectionSecret{ApiKey: value(secret.ApiKey), Token: value(secret.Token), Headers: mapValue(secret.Headers)}
		}
		request.Rules = append(request.Rules, material)
	}
	result, err := h.runtime.SetInjections(r.Context(), request)
	if err != nil {
		runtimeError(w, err)
		return
	}
	writeRuntimeInjections(w, result)
}
func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}
func mapValue(input *map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	return maps.Clone(*input)
}
func writeRuntimeInjections(w http.ResponseWriter, input *nodev1.SandboxInjections) {
	result := api.SandboxInjectionState{Rules: make([]api.SandboxInjection, 0, len(input.GetRules())), Enabled: input.GetEnabled()}
	result.Stats.Injected = input.GetInjected()
	for _, rule := range input.GetRules() {
		spec := rule.GetSpec()
		view := api.SandboxInjection{Key: rule.GetKey(), Name: rule.GetName(), Kind: api.SandboxInjectionKind(rule.GetKind()), SecretHint: new(rule.GetSecretHint())}
		if rule.GetPersistentId() != "" {
			view.PersistentId = new(rule.GetPersistentId())
		}
		view.Spec.Type = api.InjectionRuleType(spec.GetType())
		if spec.GetBaseUrl() != "" {
			view.Spec.BaseUrl = new(spec.GetBaseUrl())
		}
		if spec.GetHeaderName() != "" {
			view.Spec.HeaderName = new(spec.GetHeaderName())
		}
		if len(spec.GetIfHeaders()) > 0 {
			view.Spec.IfHeaders = new(maps.Clone(spec.GetIfHeaders()))
		}
		if len(spec.GetIfQueries()) > 0 {
			view.Spec.IfQueries = new(maps.Clone(spec.GetIfQueries()))
		}
		result.Rules = append(result.Rules, view)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
