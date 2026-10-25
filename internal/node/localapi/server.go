package localapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/DROWNING2003/Isodo/internal/sandboxfiles"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Server struct {
	files      *sandboxfiles.HTTP
	actionMu   sync.Mutex
	node       nodev1.NodeOrchestratorServer
	token      string
	catalog    *TemplateCatalog
	builder    TemplateBuilder
	builds     *buildRegistry
	index      *SandboxIndex
	injections InjectionBackend
}

// InjectionBackend is implemented by the Lima Node. The local HTTP API only
// exposes already-masked rule views; plaintext material stays in the runtime.
type InjectionBackend interface {
	SandboxInjections(sandboxID string) injection.SandboxConfig
	SetSandboxInjections(ctx context.Context, sandboxID string, rules []injection.Rule) (injection.SandboxConfig, error)
	DeleteSandboxInjections(sandboxID string) error
	InjectionSummary() map[string]int
	InjectionCACertificate() []byte
	InjectionStats(sandboxID string) injection.Stats
	InjectionProxyPort(sandboxID string) int
	RemovePersistentInjection(persistentID string) error
}

func (s *Server) SetInjections(backend InjectionBackend) { s.injections = backend }

// New serves the desktop client. Catalog and builder may be nil when the
// runtime cannot offer local templates; the base template still works.
func New(node nodev1.NodeOrchestratorServer, token string, catalog *TemplateCatalog, builder TemplateBuilder, index *SandboxIndex) (*Server, error) {
	if node == nil || strings.TrimSpace(token) == "" {
		return nil, errors.New("local API node and token are required")
	}
	if catalog == nil {
		catalog = &TemplateCatalog{specs: []TemplateSpec{{ID: BaseTemplateID, Name: "base"}}}
	}
	if index == nil {
		index = &SandboxIndex{}
	}
	server := &Server{node: node, token: token, catalog: catalog, builder: builder, builds: &buildRegistry{}, index: index}
	if provider, ok := node.(interface{ FileBackend() sandboxfiles.Backend }); ok {
		server.files = sandboxfiles.NewHTTP(provider.FileBackend(), server.fileTarget)
	}
	return server, nil
}

func NewToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate local API token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /v1/sandboxes/{sandboxID}/files", s.uploadFile)
	mux.HandleFunc("GET /v1/sandboxes/{sandboxID}/files", s.downloadFile)
	mux.HandleFunc("POST /v1/sandboxes/{sandboxID}/directories", s.makeDirectory)
	mux.HandleFunc("GET /v1/sandboxes", s.listSandboxes)
	mux.HandleFunc("POST /v1/sandboxes", s.createSandbox)
	mux.HandleFunc("GET /v1/templates", s.listTemplates)
	mux.HandleFunc("POST /v1/templates/", s.buildTemplate)
	mux.HandleFunc("GET /v1/sandboxes/{sandboxID}", s.getSandbox)
	mux.HandleFunc("PATCH /v1/sandboxes/{sandboxID}", s.patchSandbox)
	mux.HandleFunc("POST /v1/sandboxes/{sandboxID}/app-link", s.sandbox)
	mux.HandleFunc("DELETE /v1/sandboxes/{sandboxID}", s.deleteSandbox)
	mux.HandleFunc("POST /v1/sandboxes/{sandboxID}/start", s.startSandbox)
	mux.HandleFunc("POST /v1/sandboxes/{sandboxID}/stop", s.startSandbox)
	mux.HandleFunc("GET /v1/sandboxes/{sandboxID}/injections", s.getSandboxInjections)
	mux.HandleFunc("PUT /v1/sandboxes/{sandboxID}/injections", s.putSandboxInjections)
	mux.HandleFunc("DELETE /v1/sandboxes/{sandboxID}/injections", s.deleteSandboxInjections)
	mux.HandleFunc("GET /injections/summary", s.injectionSummary)
	mux.HandleFunc("DELETE /injections/persistent/{ruleID}", s.removePersistentInjection)
	mux.HandleFunc("GET /injections/ca", s.injectionCA)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.token {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "local API token is invalid", false)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status, err := s.node.GetNodeStatus(r.Context(), &emptypb.Empty{})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "NODE_UNAVAILABLE", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": status})
}

var errUnknownTemplate = errors.New("unknown template")

type createRequest struct {
	Name      string          `json:"name"`
	SandboxID string          `json:"sandbox_id"`
	Template  string          `json:"template"`
	Metadata  json.RawMessage `json:"metadata"`
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	var input createRequest
	if err := decodeBody(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be valid JSON", false)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "sandbox-" + uuid.NewString()[:8]
	}
	if len(input.Name) > 128 {
		writeError(w, 400, "INVALID_NAME", "name must contain 1..128 bytes", false)
		return
	}
	if len(input.Metadata) > 0 && !validMetadata(input.Metadata) {
		writeError(w, 400, "INVALID_METADATA", "metadata must be an object", false)
		return
	}
	sandboxID := strings.TrimSpace(input.SandboxID)
	if sandboxID == "" {
		sandboxID = "local-" + uuid.NewString()
	}
	if len(sandboxID) > 128 || !validSandboxID(sandboxID) {
		writeError(w, http.StatusBadRequest, "INVALID_SANDBOX_ID", "sandbox ID contains unsupported characters", false)
		return
	}
	templateID, templateBuildID, templateErr := s.resolveTemplate(r, input.Template)
	if templateErr != nil {
		code := http.StatusConflict
		errorCode := "TEMPLATE_NOT_READY"
		if errors.Is(templateErr, errUnknownTemplate) {
			code, errorCode = http.StatusBadRequest, "INVALID_TEMPLATE"
		}
		writeError(w, code, errorCode, templateErr.Error(), code == http.StatusConflict)
		return
	}
	operationID := uuid.NewString()
	request := &nodev1.ApplyOperationRequest{
		OperationId: operationID,
		SandboxId:   sandboxID,
		// Local sandboxes are single-tenant: the desktop client owns the first
		// generation and revision of every sandbox it creates.
		OwnershipGeneration: 1,
		Revision:            1,
		Action:              nodev1.OperationAction_OPERATION_ACTION_CREATE,
		Resources:           &nodev1.ResourceSpec{Vcpu: 2, MemoryBytes: 4 << 30, DiskBytes: 20 << 30},
		TemplateId:          templateID,
		TemplateBuildId:     templateBuildID,
	}
	request.RequestHash = requestHash(request)
	_, err := s.node.ApplyOperation(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SANDBOX_CREATE_FAILED", err.Error(), true)
		return
	}
	if err := s.index.AddDetails(sandboxID, input.Name, input.Template, templateBuildID, input.Metadata); err != nil {
		writeError(w, http.StatusInternalServerError, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	if err := s.index.SetAction(sandboxID, operationID, "create", nil); err != nil {
		writeError(w, 500, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	entry, _, _ := s.index.Next(sandboxID)
	writeJSON(w, http.StatusAccepted, s.snapshot(r.Context(), entry))
}

// resolveTemplate maps a client-visible template name onto an immutable build.
// The base needs no identifiers; anything else must already be built.
func (s *Server) resolveTemplate(r *http.Request, name string) (string, string, error) {
	id := strings.TrimSpace(name)
	if id == "" || id == BaseTemplateID {
		return "", "", nil
	}
	spec, ok := s.catalog.Find(id)
	if !ok {
		return "", "", fmt.Errorf("%w: %q", errUnknownTemplate, id)
	}
	buildID := spec.BuildID()
	ready, err := s.templateReady(r.Context(), spec.ID, buildID)
	if err != nil {
		return "", "", err
	}
	if !ready {
		return "", "", fmt.Errorf("template %q is still building", spec.Name)
	}
	return spec.ID, buildID, nil
}

// SandboxView is one desktop-owned sandbox, shaped like the control plane's
// listing so the client can merge both sources without special cases.
type SandboxResourceReporter interface {
	SandboxResourceSpec(sandboxID string) *nodev1.ResourceSpec
}

func normalizeObserved(value string) publicapi.SandboxObservedState {
	switch value {
	case "creating":
		return "starting"
	case "failed", "missing":
		return "error"
	case "deleted", "paused":
		return "stopped"
	case "requested", "preparing", "starting", "running", "stopping", "stopped", "error":
		return publicapi.SandboxObservedState(value)
	default:
		return "unknown"
	}
}

func (s *Server) snapshot(ctx context.Context, entry SandboxEntry) publicapi.Sandbox {
	metadata := entry.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	desired := entry.DesiredState
	if desired == "" {
		desired = "running"
	}
	view := publicapi.Sandbox{ID: entry.ID, Name: entry.Name, Metadata: metadata, DesiredState: publicapi.SandboxDesiredState(desired), ObservedState: "unknown", CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt}
	if entry.TemplateID != "" {
		view.TemplateID = new(entry.TemplateID)
	}
	if entry.TemplateBuildID != "" {
		view.TemplateBuildID = new(entry.TemplateBuildID)
	}
	if reporter, ok := s.node.(SandboxResourceReporter); ok {
		if resources := reporter.SandboxResourceSpec(entry.ID); resources != nil {
			view.Resources = &publicapi.SandboxResources{Vcpu: new(int(resources.Vcpu)), MemoryBytes: new(int64(resources.MemoryBytes)), DiskBytes: new(int64(resources.DiskBytes))}
		}
	}
	view.Revision = new(strconv.FormatUint(entry.Revision, 10))
	if entry.DeletionRequestedAt != nil {
		view.Deletion = &publicapi.SandboxDeletion{RequestedAt: *entry.DeletionRequestedAt}
	}
	if entry.OperationID != "" {
		view.LastAction = &publicapi.SandboxAction{Revision: *view.Revision, Type: publicapi.SandboxActionType(entry.Action), Status: "accepted", UpdatedAt: entry.UpdatedAt}
		if operation, err := s.node.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: entry.OperationID}); err == nil {
			switch operation.GetState() {
			case nodev1.OperationState_OPERATION_STATE_RUNNING:
				view.LastAction.Status = "running"
			case nodev1.OperationState_OPERATION_STATE_SUCCEEDED:
				view.LastAction.Status = "succeeded"
			case nodev1.OperationState_OPERATION_STATE_FAILED:
				view.LastAction.Status = "failed"
				view.LastAction.Error = &publicapi.ActionError{Code: operation.GetErrorCode(), Message: operation.GetMessage(), Retryable: false}
			}
			if operation.GetUpdatedAt() != nil {
				view.LastAction.UpdatedAt = operation.GetUpdatedAt().AsTime()
			}
			if view.Deletion != nil && view.LastAction.Status == "succeeded" {
				view.Deletion.CompletedAt = new(view.LastAction.UpdatedAt)
			}
		}
	}
	inspection, err := s.node.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: entry.ID, OwnershipGeneration: 1})
	if err == nil {
		view.ObservedState = normalizeObserved(inspection.GetObservedState())
		view.ObservedAt = new(time.Now().UTC())
	}
	return view
}

func pagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 100, 0
	for _, key := range []string{"limit", "offset"} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || (key == "limit" && (value < 1 || value > 100)) {
				writeError(w, 400, "INVALID_PAGINATION", "limit must be 1..100 and offset must be nonnegative", false)
				return 0, 0, false
			}
			if key == "limit" {
				limit = value
			} else {
				offset = value
			}
		}
	}
	return limit, offset, true
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	entries := s.index.Entries()
	items := make([]publicapi.Sandbox, 0)
	visible := entries[:0]
	for _, entry := range entries {
		if entry.DeletionRequestedAt == nil {
			visible = append(visible, entry)
		}
	}
	entries = visible
	for _, entry := range entries[min(offset, len(entries)):min(offset+limit, len(entries))] {
		items = append(items, s.snapshot(r.Context(), entry))
	}
	writeJSON(w, 200, publicapi.SandboxList{Items: items, Total: int64(len(entries)), Limit: limit, Offset: offset})
}

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	entry, _, ok := s.index.Next(r.PathValue("sandboxID"))
	if !ok {
		writeError(w, 404, "SANDBOX_NOT_FOUND", "sandbox not found", false)
		return
	}
	writeJSON(w, 200, s.snapshot(r.Context(), entry))
}

// startSandbox handles POST /sandboxes/{id}/start and /stop. Start resumes a
// stopped local sandbox; stop shuts the VM down while preserving its disk.
func (s *Server) startSandbox(w http.ResponseWriter, r *http.Request) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
	id := ""
	action := nodev1.OperationAction_OPERATION_ACTION_UNSPECIFIED
	errorCode := ""
	switch {
	case strings.HasSuffix(path, "/start"):
		id = strings.TrimSuffix(path, "/start")
		action = nodev1.OperationAction_OPERATION_ACTION_START
		errorCode = "SANDBOX_START_FAILED"
	case strings.HasSuffix(path, "/stop"):
		id = strings.TrimSuffix(path, "/stop")
		action = nodev1.OperationAction_OPERATION_ACTION_STOP
		errorCode = "SANDBOX_STOP_FAILED"
	default:
		writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "sandbox lifecycle route not found", false)
		return
	}
	entry, revision, ok := s.index.Next(id)
	if !ok {
		writeError(w, http.StatusNotFound, "SANDBOX_NOT_FOUND", "this Node does not own that sandbox", false)
		return
	}
	if entry.DeletionRequestedAt != nil {
		writeError(w, 409, "SANDBOX_DELETING", "sandbox deletion was requested", false)
		return
	}

	request := &nodev1.ApplyOperationRequest{
		OperationId:         uuid.NewString(),
		SandboxId:           entry.ID,
		OwnershipGeneration: 1,
		Revision:            revision,
		Action:              action,
	}
	request.RequestHash = requestHash(request)
	_, err := s.node.ApplyOperation(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadGateway, errorCode, err.Error(), true)
		return
	}
	if err := s.index.CommitDesired(id, revision, map[bool]string{true: "running", false: "stopped"}[action == nodev1.OperationAction_OPERATION_ACTION_START]); err != nil {
		writeError(w, http.StatusInternalServerError, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	actionName := "stop"
	if action == nodev1.OperationAction_OPERATION_ACTION_START {
		actionName = "start"
	}
	if err := s.index.SetAction(id, request.OperationId, actionName, nil); err != nil {
		writeError(w, 500, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	entry, _, _ = s.index.Next(id)
	writeJSON(w, http.StatusAccepted, s.snapshot(r.Context(), entry))
}

// deleteSandbox stops and removes one desktop-owned sandbox.
func (s *Server) deleteSandbox(w http.ResponseWriter, r *http.Request) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	id := r.PathValue("sandboxID")
	entry, revision, ok := s.index.Next(id)
	if !ok {
		writeError(w, http.StatusNotFound, "SANDBOX_NOT_FOUND", "this Node does not own that sandbox", false)
		return
	}
	if entry.DeletionRequestedAt != nil {
		current := s.snapshot(r.Context(), entry)
		if current.LastAction == nil || current.LastAction.Status != "failed" {
			writeJSON(w, http.StatusAccepted, current)
			return
		}
	}
	request := &nodev1.ApplyOperationRequest{
		OperationId:         uuid.NewString(),
		SandboxId:           entry.ID,
		OwnershipGeneration: 1,
		Revision:            revision,
		Action:              nodev1.OperationAction_OPERATION_ACTION_DELETE,
	}
	request.RequestHash = requestHash(request)
	if _, err := s.node.ApplyOperation(r.Context(), request); err != nil {
		writeError(w, http.StatusBadGateway, "SANDBOX_DELETE_FAILED", err.Error(), true)
		return
	}
	if err := s.index.CommitDesired(id, revision, "stopped"); err != nil {
		writeError(w, http.StatusInternalServerError, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	if s.injections != nil {
		_ = s.injections.DeleteSandboxInjections(id)
	}
	now := time.Now().UTC()
	if err := s.index.SetAction(id, request.OperationId, "delete", &now); err != nil {
		writeError(w, 500, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	entry, _, _ = s.index.Next(id)
	writeJSON(w, http.StatusAccepted, s.snapshot(r.Context(), entry))
}

func (s *Server) sandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("sandboxID")
	if _, _, ok := s.index.Next(id); !ok {
		writeError(w, 404, "SANDBOX_NOT_FOUND", "sandbox not found", false)
		return
	}
	link, err := s.node.CreateSandboxAppLink(r.Context(), &nodev1.CreateSandboxAppLinkRequest{SandboxId: id})
	if err != nil {
		writeError(w, 502, "SANDBOX_APP_UNAVAILABLE", err.Error(), true)
		return
	}
	expires := link.GetExpiresAt().AsTime()
	writeJSON(w, 200, publicapi.SandboxAppLink{SandboxId: id, Service: "t3", Url: link.GetUrl(), PairingUrl: link.GetUrl(), Status: "ready", ExpiresAt: expires, Capabilities: &[]string{"terminal", "files"}})
}

func validSandboxID(value string) bool {
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return value != ""
}

func requestHash(request *nodev1.ApplyOperationRequest) string {
	data, _ := json.Marshal(request)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, errorCode, message string, retryable bool) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": errorCode, "message": message, "retryable": retryable}})
}

func validMetadata(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}
func decodeBody(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON value")
	}
	return nil
}
func (s *Server) patchSandbox(w http.ResponseWriter, r *http.Request) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	id := r.PathValue("sandboxID")
	entry, _, ok := s.index.Next(id)
	if !ok || entry.DeletionRequestedAt != nil {
		writeError(w, 404, "SANDBOX_NOT_FOUND", "sandbox not found", false)
		return
	}
	var patch publicapi.PatchSandbox
	if err := decodeBody(w, r, &patch); err != nil {
		writeError(w, 400, "INVALID_JSON", "invalid sandbox patch", false)
		return
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" || len(name) > 128 {
			writeError(w, 400, "INVALID_SANDBOX_NAME", "name must contain 1..128 bytes", false)
			return
		}
		entry.Name = name
	}
	if patch.Metadata != nil {
		if !validMetadata(*patch.Metadata) {
			writeError(w, 400, "INVALID_METADATA", "metadata must be an object", false)
			return
		}
		entry.Metadata = *patch.Metadata
	}
	if err := s.index.UpdateProperties(id, entry.Name, entry.Metadata); err != nil {
		writeError(w, 500, "SANDBOX_INDEX_FAILED", "cannot save sandbox properties", true)
		return
	}
	entry, _, _ = s.index.Next(id)
	writeJSON(w, 200, s.snapshot(r.Context(), entry))
}
