package localapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
)

const maxAgentConfigBytes = 256 << 10

type agentConfigRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) putAgentConfig(w http.ResponseWriter, r *http.Request) {
	if s.agentFiles == nil {
		writeError(w, http.StatusNotImplemented, "AGENT_CONFIG_UNSUPPORTED", "Agent configuration writing is not enabled on this Node", false)
		return
	}
	sandboxID := r.PathValue("sandboxID")
	if !validSandboxID(sandboxID) {
		writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_ID", "sandbox ID contains unsupported characters", false)
		return
	}
	var input agentConfigRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentConfigBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must contain path and content", false)
		return
	}
	input.Path = strings.TrimSpace(input.Path)
	if !safeAgentPath(input.Path) {
		writeError(w, http.StatusBadRequest, "INVALID_AGENT_PATH", "Agent configuration path must stay inside the sandbox workspace or home", false)
		return
	}
	if len(input.Content) > maxAgentConfigBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "AGENT_CONFIG_TOO_LARGE", "Agent configuration is too large", false)
		return
	}
	result, err := s.agentFiles.WriteAgentConfig(r.Context(), sandboxID, input.Path, []byte(input.Content))
	if err != nil {
		writeError(w, http.StatusBadGateway, "AGENT_CONFIG_WRITE_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func safeAgentPath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\x00\r\n") || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(strings.TrimPrefix(path, "~/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	return strings.HasPrefix(clean, ".config/") || strings.HasPrefix(clean, ".codex/") ||
		strings.HasPrefix(clean, ".claude/") || strings.HasPrefix(clean, ".cursor/") ||
		strings.HasPrefix(clean, ".config/opencode/") || strings.HasPrefix(clean, ".config/grok/") ||
		strings.HasPrefix(clean, "workspace/")
}

func digestAgentConfig(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

var errAgentConfigPath = errors.New("invalid Agent configuration path")
