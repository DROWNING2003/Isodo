package sandbox

import (
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/sandboxfiles"
	"net/http"
)

func (h *Handler) SetFiles(backend sandboxfiles.Backend) {
	h.files = sandboxfiles.NewHTTP(backend, func(w http.ResponseWriter, r *http.Request, id string) (*nodev1.SandboxTarget, bool) {
		return h.resolveRuntimeTarget(w, r, id, false)
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
