package localapi

import (
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"net/http"
)

func (s *Server) fileTarget(w http.ResponseWriter, r *http.Request, id string) (*nodev1.SandboxTarget, bool) {
	entry, _, ok := s.index.Next(id)
	if !ok {
		writeError(w, 404, "SANDBOX_NOT_FOUND", "this Node does not own that sandbox", false)
		return nil, false
	}
	if entry.DeletionRequestedAt != nil {
		writeError(w, 409, "SANDBOX_DELETING", "the sandbox is being deleted", false)
		return nil, false
	}
	return &nodev1.SandboxTarget{SandboxId: id, OwnershipGeneration: 1, Revision: entry.Revision}, true
}
func (s *Server) filesReady(w http.ResponseWriter) bool {
	if s.files == nil {
		writeError(w, 501, "FILES_UNSUPPORTED", "this Node does not expose Files", false)
		return false
	}
	return true
}
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	if s.filesReady(w) {
		s.files.Upload(w, r, r.PathValue("sandboxID"))
	}
}
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	if s.filesReady(w) {
		s.files.Download(w, r, r.PathValue("sandboxID"))
	}
}
func (s *Server) makeDirectory(w http.ResponseWriter, r *http.Request) {
	if s.filesReady(w) {
		s.files.MakeDirectory(w, r, r.PathValue("sandboxID"))
	}
}
