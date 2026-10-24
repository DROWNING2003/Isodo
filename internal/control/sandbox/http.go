package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrNotFound = errors.New("sandbox not found")

var sandboxIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	defaultSandboxPageSize = 50
	maxSandboxPageSize     = 100
)

type Sandbox struct {
	ID              string          `json:"id"`
	TenantID        string          `json:"-"`
	Name            string          `json:"name"`
	Metadata        json.RawMessage `json:"metadata"`
	DesiredState    string          `json:"desired_state"`
	ObservedState   string          `json:"observed_state"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	TemplateID      *string         `json:"template_id,omitempty"`
	TemplateBuildID *string         `json:"template_build_id,omitempty"`
	TemplateDigest  *string         `json:"template_digest,omitempty"`
}

type Patch struct {
	Name     *string
	Metadata *json.RawMessage
}

type Store interface {
	CreateSandbox(ctx context.Context, tenantID, name string, metadata json.RawMessage, templateRef string) (Sandbox, error)
	ListSandboxes(ctx context.Context, tenantID string, limit, offset int) ([]Sandbox, int64, error)
	GetSandbox(ctx context.Context, tenantID, sandboxID string) (Sandbox, error)
	UpdateSandbox(ctx context.Context, tenantID, sandboxID string, patch Patch) (Sandbox, error)
	DeleteSandbox(ctx context.Context, tenantID, sandboxID string) error
}

type Handler struct {
	store     Store
	appLink   AppLinkIssuer
	localLima bool
	webOrigin string
}

type AppLinkIssuer interface {
	CreateSandboxAppLink(context.Context, string) (string, time.Time, error)
}

func NewHandler(store Store, appLinks ...AppLinkIssuer) *Handler {
	return NewHandlerForBackend(store, "chv", appLinks...)
}

func NewHandlerForBackend(store Store, backend string, appLinks ...AppLinkIssuer) *Handler {
	handler := &Handler{store: store, localLima: backend == "lima"}
	if len(appLinks) > 0 {
		handler.appLink = appLinks[0]
	}
	return handler
}

func NewHandlerForBackendWithWebOrigin(store Store, backend, webOrigin string, appLinks ...AppLinkIssuer) *Handler {
	handler := NewHandlerForBackend(store, backend, appLinks...)
	handler.webOrigin = strings.TrimRight(webOrigin, "/")
	return handler
}

type createRequest = publicapi.CreateSandboxJSONRequestBody
type patchRequest = publicapi.UpdateSandboxJSONRequestBody

func (h *Handler) CreateSandbox(w http.ResponseWriter, r *http.Request) {
	var request createRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	if !validName(name) {
		writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_NAME", "name must contain 1 to 128 bytes.")
		return
	}
	metadata, ok := normalizeMetadata(request.Metadata)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_METADATA", "metadata must be a JSON object no larger than 64 KiB.")
		return
	}
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Casdoor bearer token is required.")
		return
	}
	reference := ""
	if request.Template != nil {
		reference = *request.Template
	}
	if _, _, err := ParseTemplateReference(reference); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TEMPLATE", err.Error())
		return
	}
	if h.localLima {
		if reference != "" && reference != "linux-base" {
			writeError(w, http.StatusUnprocessableEntity, "TEMPLATE_UNSUPPORTED", "The local Lima backend currently uses its built-in Linux base.")
			return
		}
		reference = LocalLimaTemplateReference
	}
	sandbox, err := h.store.CreateSandbox(r.Context(), identity.TenantID, name, metadata, reference)
	if errors.Is(err, ErrTemplateUnavailable) {
		writeError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "The template does not exist or has no accessible ready build.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The sandbox could not be created.")
		return
	}
	writeJSON(w, http.StatusCreated, sandboxResponse(sandbox))
}

func (h *Handler) ListSandboxes(w http.ResponseWriter, r *http.Request, params publicapi.ListSandboxesParams) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Casdoor bearer token is required.")
		return
	}
	limit, offset := defaultSandboxPageSize, 0
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 || limit > maxSandboxPageSize {
			writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 100.")
			return
		}
	}
	if params.Offset != nil {
		offset = *params.Offset
		if offset < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_OFFSET", "offset must be non-negative.")
			return
		}
	}
	items, total, err := h.store.ListSandboxes(r.Context(), identity.TenantID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The sandbox list could not be loaded.")
		return
	}
	result := make([]publicapi.Sandbox, 0, len(items))
	for _, item := range items {
		result = append(result, sandboxResponse(item))
	}
	writeJSON(w, http.StatusOK, publicapi.SandboxList{Items: result, Total: total, Limit: limit, Offset: offset})
}

func (h *Handler) GetSandbox(w http.ResponseWriter, r *http.Request, id string) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Casdoor bearer token is required.")
		return
	}
	if !sandboxIDPattern.MatchString(id) {
		writeSandboxNotFound(w)
		return
	}
	sandbox, err := h.store.GetSandbox(r.Context(), identity.TenantID, id)
	if errors.Is(err, ErrNotFound) {
		writeSandboxNotFound(w)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The sandbox could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, sandboxResponse(sandbox))
}

// GetSandboxApp redirects an authenticated sandbox owner into T3's own pairing
// flow. The short-lived T3 credential stays in the URL fragment and is never
// sent back to this API in an HTTP request.
func (h *Handler) GetSandboxApp(w http.ResponseWriter, r *http.Request, id string) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Isodo session is required.")
		return
	}
	if identity.Credential == "api_key" {
		writeError(w, http.StatusForbidden, "INTERACTIVE_SESSION_REQUIRED", "Open the sandbox app from an authenticated Isodo session.")
		return
	}
	if !sandboxIDPattern.MatchString(id) {
		writeSandboxNotFound(w)
		return
	}
	if _, err := h.store.GetSandbox(r.Context(), identity.TenantID, id); errors.Is(err, ErrNotFound) {
		writeSandboxNotFound(w)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The sandbox could not be loaded.")
		return
	}
	if h.appLink == nil {
		writeError(w, http.StatusServiceUnavailable, "SANDBOX_APP_UNAVAILABLE", "The sandbox application service is not configured.")
		return
	}
	appURL, expiresAt, err := h.appLink.CreateSandboxAppLink(r.Context(), id)
	if err != nil {
		code, message := http.StatusBadGateway, "The sandbox application could not be opened."
		if grpcStatus, ok := status.FromError(err); ok {
			switch grpcStatus.Code() {
			case codes.NotFound:
				writeSandboxNotFound(w)
				return
			case codes.FailedPrecondition:
				code, message = http.StatusConflict, "The sandbox must be running before its application can be opened."
			case codes.Unavailable, codes.DeadlineExceeded:
				code, message = http.StatusServiceUnavailable, "The sandbox application is temporarily unavailable."
			}
		}
		writeError(w, code, "SANDBOX_APP_UNAVAILABLE", message)
		return
	}
	parsed, err := url.Parse(appURL)
	if err != nil || parsed.Scheme != "http" || !strings.HasSuffix(parsed.Hostname(), ".localhost") || parsed.Path != "/pair" || parsed.Fragment == "" || !expiresAt.After(time.Now()) {
		writeError(w, http.StatusBadGateway, "INVALID_SANDBOX_APP_LINK", "The Node returned an invalid sandbox application link.")
		return
	}
	// The Node issues a one-time T3 credential and a direct Guest URL. Open
	// hosted pairing in Isodo instead: that existing T3 flow stores the remote
	// environment in the signed-in browser and then opens its workspace.
	if h.webOrigin != "" {
		webURL, err := url.Parse(h.webOrigin)
		if err != nil || webURL.Scheme == "" || webURL.Host == "" {
			writeError(w, http.StatusInternalServerError, "SANDBOX_APP_UNAVAILABLE", "The Isodo web origin is invalid.")
			return
		}
		webURL.Path = "/pair"
		webURL.RawQuery = url.Values{"host": []string{parsed.Host}}.Encode()
		webURL.Fragment = parsed.Fragment
		parsed = webURL
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, parsed.String(), http.StatusSeeOther)
}

func (h *Handler) UpdateSandbox(w http.ResponseWriter, r *http.Request, id string) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Casdoor bearer token is required.")
		return
	}
	if !sandboxIDPattern.MatchString(id) {
		writeSandboxNotFound(w)
		return
	}
	var request patchRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Name == nil && request.Metadata == nil {
		writeError(w, http.StatusBadRequest, "EMPTY_PATCH", "at least one mutable field is required.")
		return
	}
	patch := Patch{Metadata: request.Metadata}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if !validName(name) {
			writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_NAME", "name must contain 1 to 128 bytes.")
			return
		}
		patch.Name = &name
	}
	if request.Metadata != nil {
		metadata, ok := normalizeMetadata(*request.Metadata)
		if !ok {
			writeError(w, http.StatusBadRequest, "INVALID_METADATA", "metadata must be a JSON object no larger than 64 KiB.")
			return
		}
		patch.Metadata = &metadata
	}
	sandbox, err := h.store.UpdateSandbox(r.Context(), identity.TenantID, id, patch)
	if errors.Is(err, ErrNotFound) {
		writeSandboxNotFound(w)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The sandbox could not be updated.")
		return
	}
	writeJSON(w, http.StatusOK, sandboxResponse(sandbox))
}

func (h *Handler) DeleteSandbox(w http.ResponseWriter, r *http.Request, id string) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.TenantID == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Casdoor bearer token is required.")
		return
	}
	if !sandboxIDPattern.MatchString(id) {
		writeSandboxNotFound(w)
		return
	}
	err := h.store.DeleteSandbox(r.Context(), identity.TenantID, id)
	if errors.Is(err, ErrNotFound) {
		writeSandboxNotFound(w)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The sandbox could not be deleted.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be a valid JSON object with known fields.")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must contain exactly one JSON value.")
		return false
	}
	return true
}

func normalizeMetadata(metadata json.RawMessage) (json.RawMessage, bool) {
	if len(metadata) == 0 {
		return json.RawMessage(`{}`), true
	}
	if len(metadata) > 64<<10 || !json.Valid(metadata) {
		return nil, false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &object); err != nil || object == nil {
		return nil, false
	}
	encoded, err := json.Marshal(object)
	return encoded, err == nil
}

func validName(name string) bool {
	return len(name) > 0 && len(name) <= 128
}

func writeSandboxNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "SANDBOX_NOT_FOUND", "The sandbox was not found.")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sandboxResponse(s Sandbox) publicapi.Sandbox {
	return publicapi.Sandbox{ID: s.ID, Name: s.Name, TemplateID: s.TemplateID, TemplateBuildID: s.TemplateBuildID, TemplateDigest: s.TemplateDigest, Metadata: s.Metadata, DesiredState: s.DesiredState,
		ObservedState: s.ObservedState, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
