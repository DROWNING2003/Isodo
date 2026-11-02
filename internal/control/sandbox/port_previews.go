package sandbox

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
)

// Anonymous resolution is restricted to HTTP Preview. It is never exposed by
// account CRUD routes and cannot supply a caller-chosen tenant or Node address.
type PortPreviewStore interface {
	LookupPreviewSandbox(context.Context, string) (Sandbox, error)
	GetPortPreview(context.Context, string, string, uint16) (preview.PortPolicy, error)
	SetPortPreview(context.Context, string, string, uint16, string) (preview.PortPolicy, error)
}

func (h *Handler) portPolicy(ctx context.Context, tenant, id string, port uint16) (preview.PortPolicy, error) {
	store, ok := h.store.(PortPreviewStore)
	if !ok {
		return preview.PortPolicy{}, errors.New("preview policy store unavailable")
	}
	return store.GetPortPreview(ctx, tenant, id, port)
}
func (h *Handler) ResolveHTTPPreview(ctx context.Context, id string, port uint16) (preview.HTTPAccess, error) {
	if !preview.ValidHTTPPort(port) || !sandboxIDPattern.MatchString(id) {
		return preview.HTTPAccess{}, ErrNotFound
	}
	store, ok := h.store.(PortPreviewStore)
	if !ok {
		return preview.HTTPAccess{}, errors.New("preview policy store unavailable")
	}
	item, err := store.LookupPreviewSandbox(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			err = preview.ErrRuntimeUnavailable
		}
		return preview.HTTPAccess{}, err
	}
	policy, err := store.GetPortPreview(ctx, item.TenantID, id, port)
	if err != nil {
		return preview.HTTPAccess{}, err
	}
	access := preview.HTTPAccess{PortPolicy: policy}
	// Private anonymous requests need no Node RPC; their signed grant supplies the
	// runtime identity and AuthorizeHTTPPreview still revalidates it before relay.
	if policy.Visibility != "public" {
		return access, nil
	}
	access.Prepare = func(ctx context.Context) (preview.HTTPGrant, error) {
		if h.previewNode == nil || item.NodeID != h.previewNode.NodeID() || item.Deletion != nil || item.DesiredState != "running" || item.ObservedState != "running" {
			return preview.HTTPGrant{}, preview.ErrRuntimeUnavailable
		}
		revision, err := strconv.ParseUint(item.Revision, 10, 64)
		if err != nil {
			return preview.HTTPGrant{}, err
		}
		observed, err := h.previewNode.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: id, OwnershipGeneration: item.OwnershipGeneration})
		if err != nil {
			return preview.HTTPGrant{}, err
		}
		if observed == nil || observed.ObservedState != "running" || observed.SandboxId != id || observed.OwnershipGeneration != item.OwnershipGeneration || observed.BootId == "" || observed.GuestEpoch == "" {
			return preview.HTTPGrant{}, preview.ErrRuntimeUnavailable
		}
		return preview.HTTPGrant{Public: true, PolicyRevision: policy.Revision, TenantID: item.TenantID, Subject: "public", SandboxID: id, NodeID: item.NodeID, Generation: item.OwnershipGeneration, Revision: revision, BootID: observed.BootId, GuestEpoch: observed.GuestEpoch, Port: port}, nil
	}
	return access, nil
}
func (h *Handler) previewPolicyRequest(w http.ResponseWriter, r *http.Request, id string, port int) (PortPreviewStore, string, uint16, bool) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, 401, "UNAUTHENTICATED", "Authentication required.")
		return nil, "", 0, false
	}
	if !sandboxIDPattern.MatchString(id) {
		writeSandboxNotFound(w)
		return nil, "", 0, false
	}
	if port < 1 || port > 65535 || !preview.ValidHTTPPort(uint16(port)) {
		writeError(w, 400, "INVALID_PREVIEW_PORT", "Management ports cannot be previewed.")
		return nil, "", 0, false
	}
	store, ok := h.store.(PortPreviewStore)
	if !ok {
		writeError(w, 503, "PREVIEW_UNAVAILABLE", "Preview policies are unavailable.")
		return nil, "", 0, false
	}
	return store, identity.TenantID, uint16(port), true
}
func (h *Handler) GetSandboxPortPreview(w http.ResponseWriter, r *http.Request, id string, port int) {
	store, tenant, p, ok := h.previewPolicyRequest(w, r, id, port)
	if !ok {
		return
	}
	policy, err := store.GetPortPreview(r.Context(), tenant, id, p)
	if err != nil {
		h.portPolicyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, policy)
}
func (h *Handler) SetSandboxPortPreview(w http.ResponseWriter, r *http.Request, id string, port int) {
	store, tenant, p, ok := h.previewPolicyRequest(w, r, id, port)
	if !ok {
		return
	}
	var input struct {
		Visibility string `json:"visibility"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Visibility != "public" && input.Visibility != "private" {
		writeError(w, 400, "INVALID_VISIBILITY", "visibility must be public or private.")
		return
	}
	policy, err := store.SetPortPreview(r.Context(), tenant, id, p, input.Visibility)
	if err != nil {
		h.portPolicyError(w, err)
		return
	}
	if h.httpPreview != nil {
		h.httpPreview.CancelHTTPPort(id, p)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, policy)
}
func (h *Handler) portPolicyError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeSandboxNotFound(w)
		return
	}
	writeError(w, 503, "PREVIEW_UNAVAILABLE", "Preview policy operation failed.")
}
