package sandbox

import (
	"net/http"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/sandboxfiles"
)

func (h *Handler) SetFiles(backend sandboxfiles.Backend) {
	h.files = sandboxfiles.NewHTTP(backend, func(w http.ResponseWriter, r *http.Request, id string) (*nodev1.SandboxTarget, bool) {
		target, item, ok := h.resolveRuntimeTargetWithSandbox(w, r, id, false)
		if !ok {
			return nil, false
		}
		if item.DesiredState != "running" || item.ObservedState != "running" {
			writeError(w, 409, "SANDBOX_STATE_CONFLICT", "Sandbox must be running for Files.")
			return nil, false
		}
		return target, true
	})
}
func (h *Handler) filesReady(w http.ResponseWriter) bool {
	if h.files == nil {
		writeError(w, 503, "FILES_UNAVAILABLE", "Sandbox files are not configured.")
		return false
	}
	return true
}
func (h *Handler) UploadSandboxFile(w http.ResponseWriter, r *http.Request, id string, _ api.UploadSandboxFileParams) {
	if h.filesReady(w) {
		h.files.Upload(w, r, id)
	}
}
func (h *Handler) DownloadSandboxFile(w http.ResponseWriter, r *http.Request, id string, _ api.DownloadSandboxFileParams) {
	if h.filesReady(w) {
		h.files.Download(w, r, id)
	}
}
func (h *Handler) MakeSandboxDirectory(w http.ResponseWriter, r *http.Request, id string) {
	if h.filesReady(w) {
		h.files.MakeDirectory(w, r, id)
	}
}

func (h *Handler) StatSandboxFile(w http.ResponseWriter, r *http.Request, id string, _ api.StatSandboxFileParams) {
	if h.filesReady(w) {
		h.files.Stat(w, r, id)
	}
}
func (h *Handler) ListSandboxDirectory(w http.ResponseWriter, r *http.Request, id string, _ api.ListSandboxDirectoryParams) {
	if h.filesReady(w) {
		h.files.ListDirectory(w, r, id)
	}
}
func (h *Handler) RemoveSandboxFile(w http.ResponseWriter, r *http.Request, id string, _ api.RemoveSandboxFileParams) {
	if h.filesReady(w) {
		h.files.Remove(w, r, id)
	}
}
func (h *Handler) MoveSandboxFile(w http.ResponseWriter, r *http.Request, id string) {
	if h.filesReady(w) {
		h.files.Move(w, r, id)
	}
}
