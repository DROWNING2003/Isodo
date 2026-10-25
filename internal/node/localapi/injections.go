package localapi

import (
	"encoding/json"
	"net/http"

	"github.com/DROWNING2003/Isodo/internal/node/injection"
)

const (
	maxInjectionRules      = 32
	maxInjectionBodyBytes  = 256 << 10
	maxInjectionStringSize = 8192
)

type injectionResponse struct {
	Rules     []injection.Rule `json:"rules"`
	Enabled   bool             `json:"enabled"`
	ProxyPort int              `json:"proxy_port,omitempty"`
	Stats     injection.Stats  `json:"stats"`
	UpdatedAt string           `json:"updated_at,omitempty"`
}

func (s *Server) injectionBackend() (InjectionBackend, bool) {
	return s.injections, s.injections != nil
}

func (s *Server) getSandboxInjections(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.injectionBackend()
	if !ok {
		writeError(w, http.StatusNotImplemented, "INJECTIONS_UNSUPPORTED", "secure connections are not enabled on this Node", false)
		return
	}
	sandboxID := r.PathValue("sandboxID")
	if !validSandboxID(sandboxID) {
		writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_ID", "sandbox ID contains unsupported characters", false)
		return
	}
	config := backend.SandboxInjections(sandboxID)
	writeJSON(w, http.StatusOK, injectionResponse{
		Rules: config.Rules, Enabled: config.Enabled,
		ProxyPort: backend.InjectionProxyPort(sandboxID),
		Stats:     backend.InjectionStats(sandboxID),
	})
}

func (s *Server) putSandboxInjections(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.injectionBackend()
	if !ok {
		writeError(w, http.StatusNotImplemented, "INJECTIONS_UNSUPPORTED", "secure connections are not enabled on this Node", false)
		return
	}
	sandboxID := r.PathValue("sandboxID")
	if !validSandboxID(sandboxID) {
		writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_ID", "sandbox ID contains unsupported characters", false)
		return
	}
	var input struct {
		Rules []injection.Rule `json:"rules"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInjectionBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be a JSON object with a rules array", false)
		return
	}
	if len(input.Rules) > maxInjectionRules {
		writeError(w, http.StatusBadRequest, "TOO_MANY_RULES", "at most 32 rules can be attached to one sandbox", false)
		return
	}
	for _, rule := range input.Rules {
		if len(rule.Name) > 128 || len(rule.Key) > 128 || len(rule.Spec.BaseURL) > 2048 {
			writeError(w, http.StatusBadRequest, "INVALID_RULE", "rule fields exceed their size limit", false)
			return
		}
		for _, value := range rule.Secret.Headers {
			if len(value) > maxInjectionStringSize {
				writeError(w, http.StatusBadRequest, "INVALID_RULE", "rule secret exceeds its size limit", false)
				return
			}
		}
		if len(rule.Secret.APIKey) > maxInjectionStringSize || len(rule.Secret.Token) > maxInjectionStringSize {
			writeError(w, http.StatusBadRequest, "INVALID_RULE", "rule secret exceeds its size limit", false)
			return
		}
	}
	config, err := backend.SetSandboxInjections(r.Context(), sandboxID, input.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_RULE", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, injectionResponse{
		Rules: config.Rules, Enabled: config.Enabled,
		ProxyPort: backend.InjectionProxyPort(sandboxID),
		Stats:     backend.InjectionStats(sandboxID),
	})
}

func (s *Server) deleteSandboxInjections(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.injectionBackend()
	if !ok {
		writeError(w, http.StatusNotImplemented, "INJECTIONS_UNSUPPORTED", "secure connections are not enabled on this Node", false)
		return
	}
	sandboxID := r.PathValue("sandboxID")
	if !validSandboxID(sandboxID) {
		writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_ID", "sandbox ID contains unsupported characters", false)
		return
	}
	if err := backend.DeleteSandboxInjections(sandboxID); err != nil {
		writeError(w, http.StatusInternalServerError, "INJECTION_DELETE_FAILED", err.Error(), true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) injectionSummary(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.injectionBackend()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"persistent": map[string]int{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"persistent": backend.InjectionSummary()})
}

func (s *Server) removePersistentInjection(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.injectionBackend()
	if !ok {
		writeError(w, http.StatusNotImplemented, "INJECTIONS_UNSUPPORTED", "secure connections are not enabled on this Node", false)
		return
	}
	ruleID := r.PathValue("ruleID")
	if ruleID == "" || len(ruleID) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_RULE_ID", "rule ID is invalid", false)
		return
	}
	if err := backend.RemovePersistentInjection(ruleID); err != nil {
		writeError(w, http.StatusInternalServerError, "INJECTION_REVOKE_FAILED", err.Error(), true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) injectionCA(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.injectionBackend()
	if !ok || len(backend.InjectionCACertificate()) == 0 {
		writeError(w, http.StatusNotImplemented, "INJECTIONS_UNSUPPORTED", "secure connections are not enabled on this Node", false)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(backend.InjectionCACertificate())
}
