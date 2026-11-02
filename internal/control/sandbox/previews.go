package sandbox

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
)

type PreviewInspector interface {
	NodeID() string
	InspectSandbox(context.Context, *nodev1.InspectSandboxRequest) (*nodev1.SandboxInspection, error)
}

func (h *Handler) SetHTTPPreview(p *preview.Publisher, node PreviewInspector) {
	h.httpPreview = p
	h.previewNode = node
	p.ConfigureHTTPAccess(h.ResolveHTTPPreview)
}

// AuthorizeHTTPPreview checks tenant ownership and the current runtime on every
// HTTP request, handshake, and while an active stream is being relayed.
func (h *Handler) AuthorizeHTTPPreview(ctx context.Context, g preview.HTTPGrant) error {
	if h.previewNode == nil || g.TenantID == "" || g.Subject == "" || g.NodeID != h.previewNode.NodeID() {
		return errors.New("preview Node unavailable")
	}
	policy, err := h.portPolicy(ctx, g.TenantID, g.SandboxID, g.Port)
	if err != nil || policy.Revision != g.PolicyRevision || (g.Public && policy.Visibility != "public") || (!g.Public && policy.Visibility != "private") {
		return errors.New("preview visibility changed")
	}
	item, err := h.store.GetSandbox(ctx, g.TenantID, g.SandboxID)
	if err != nil {
		return err
	}
	if item.NodeID != g.NodeID || item.TenantID != g.TenantID || item.Deletion != nil || item.DesiredState != "running" || item.ObservedState != "running" || item.OwnershipGeneration != g.Generation || item.Revision != strconv.FormatUint(g.Revision, 10) {
		return errors.New("preview sandbox runtime changed")
	}
	observed, err := h.previewNode.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: g.SandboxID, OwnershipGeneration: g.Generation})
	if err != nil {
		return err
	}
	if observed == nil || observed.SandboxId != g.SandboxID || observed.OwnershipGeneration != g.Generation || observed.ObservedState != "running" || observed.BootId != g.BootID || observed.GuestEpoch != g.GuestEpoch {
		return errors.New("preview Guest identity changed")
	}
	return nil
}

func (h *Handler) CreateSandboxPreview(w http.ResponseWriter, r *http.Request, id string) {
	id = strings.ToLower(id)
	target, item, ok := h.resolveRuntimeTargetWithSandbox(w, r, id, false)
	if !ok {
		return
	}
	if item.DesiredState != "running" || item.ObservedState != "running" {
		writeError(w, 409, "SANDBOX_NOT_RUNNING", "Previews require a running sandbox.")
		return
	}
	if h.httpPreview == nil || h.previewNode == nil {
		writeError(w, 503, "PREVIEW_UNAVAILABLE", "HTTP previews are not configured.")
		return
	}
	var input struct {
		Port int `json:"port"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Port < 1 || input.Port > 65535 || !preview.ValidHTTPPort(uint16(input.Port)) {
		writeError(w, 400, "INVALID_PREVIEW_PORT", "Choose a port between 1 and 65535, excluding runtime ports 31024, 31080 and 38080.")
		return
	}
	observed, err := h.previewNode.InspectSandbox(r.Context(), &nodev1.InspectSandboxRequest{SandboxId: id, OwnershipGeneration: target.OwnershipGeneration})
	if err != nil {
		runtimeError(w, err)
		return
	}
	if observed == nil {
		writeError(w, 503, "PREVIEW_UNAVAILABLE", "Node inspection is unavailable.")
		return
	}
	identity, _ := auth.IdentityFromContext(r.Context())
	policy, err := h.portPolicy(r.Context(), identity.TenantID, id, uint16(input.Port))
	if err != nil {
		h.portPolicyError(w, err)
		return
	}
	link, err := h.httpPreview.IssueHTTP(r.Context(), preview.HTTPGrant{Public: policy.Visibility == "public", PolicyRevision: policy.Revision, TenantID: identity.TenantID, Subject: identity.Subject, SandboxID: id, NodeID: item.NodeID, Generation: target.OwnershipGeneration, Revision: target.Revision, BootID: observed.BootId, GuestEpoch: observed.GuestEpoch, Port: uint16(input.Port)})
	if err != nil {
		writeError(w, 409, "PREVIEW_RUNTIME_CHANGED", "The sandbox runtime is unavailable or changed. Retry after it is running.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, link)
}
