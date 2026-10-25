package localapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Server struct {
	node       nodev1.NodeOrchestratorServer
	token      string
	catalog    *TemplateCatalog
	builder    TemplateBuilder
	builds     *buildRegistry
	index      *SandboxIndex
	injections InjectionBackend
	agentFiles AgentConfigWriter
}

// AgentConfigWriter writes already-redacted Agent configuration content into a
// running local sandbox. The implementation owns guest transport and never
// receives service connection secrets.
type AgentConfigWriter interface {
	WriteAgentConfig(ctx context.Context, sandboxID, path string, content []byte) (AgentConfigWriteResult, error)
}

type AgentConfigWriteResult struct {
	Path            string `json:"path"`
	Sha256          string `json:"sha256"`
	Bytes           int    `json:"bytes"`
	RequiresRestart bool   `json:"requires_restart"`
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

func (s *Server) SetAgentConfigWriter(writer AgentConfigWriter) { s.agentFiles = writer }

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
	return &Server{node: node, token: token, catalog: catalog, builder: builder, builds: &buildRegistry{}, index: index}, nil
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
	mux.HandleFunc("GET /sandboxes", s.listSandboxes)
	mux.HandleFunc("POST /sandboxes", s.createSandbox)
	mux.HandleFunc("GET /templates", s.listTemplates)
	mux.HandleFunc("POST /templates/", s.buildTemplate)
	mux.HandleFunc("GET /operations/", s.getOperation)
	mux.HandleFunc("GET /sandboxes/", s.sandbox)
	mux.HandleFunc("DELETE /sandboxes/", s.deleteSandbox)
	mux.HandleFunc("POST /sandboxes/", s.startSandbox)
	mux.HandleFunc("GET /sandboxes/{sandboxID}/injections", s.getSandboxInjections)
	mux.HandleFunc("PUT /sandboxes/{sandboxID}/injections", s.putSandboxInjections)
	mux.HandleFunc("DELETE /sandboxes/{sandboxID}/injections", s.deleteSandboxInjections)
	mux.HandleFunc("PUT /sandboxes/{sandboxID}/agent-config", s.putAgentConfig)
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
	Name      string `json:"name"`
	SandboxID string `json:"sandbox_id"`
	Template  string `json:"template"`
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	var input createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be valid JSON", false)
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
	result, err := s.node.ApplyOperation(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SANDBOX_CREATE_FAILED", err.Error(), true)
		return
	}
	if err := s.index.Add(sandboxID, input.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sandbox_id": sandboxID, "operation": result.GetOperation()})
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

type SandboxView struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	State     string               `json:"state"`
	Local     bool                 `json:"local"`
	Resources *nodev1.ResourceSpec `json:"resources,omitempty"`
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	items := make([]SandboxView, 0, len(s.index.Entries()))
	for _, entry := range s.index.Entries() {
		view := SandboxView{ID: entry.ID, Name: entry.Name, State: "unknown", Local: true}
		if reporter, ok := s.node.(SandboxResourceReporter); ok {
			view.Resources = reporter.SandboxResourceSpec(entry.ID)
		}
		inspection, err := s.node.InspectSandbox(r.Context(), &nodev1.InspectSandboxRequest{
			SandboxId:           entry.ID,
			OwnershipGeneration: 1,
		})
		if err == nil {
			view.State = inspection.GetObservedState()
			if view.State == "deleted" || view.State == "" {
				_ = s.index.Remove(entry.ID)
				continue
			}
			if view.Name == "" {
				view.Name = entry.ID
			}
		} else if status.Code(err) == codes.NotFound {
			// The Node no longer owns it; drop it instead of showing a ghost.
			_ = s.index.Remove(entry.ID)
			continue
		}
		items = append(items, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// startSandbox handles POST /sandboxes/{id}/start and /stop. Start resumes a
// stopped local sandbox; stop shuts the VM down while preserving its disk.
func (s *Server) startSandbox(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
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
	request := &nodev1.ApplyOperationRequest{
		OperationId:         uuid.NewString(),
		SandboxId:           entry.ID,
		OwnershipGeneration: 1,
		Revision:            revision,
		Action:              action,
	}
	request.RequestHash = requestHash(request)
	result, err := s.node.ApplyOperation(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadGateway, errorCode, err.Error(), true)
		return
	}
	if err := s.index.Commit(id, revision); err != nil {
		writeError(w, http.StatusInternalServerError, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sandbox_id": entry.ID, "operation": result.GetOperation()})
}

// deleteSandbox stops and removes one desktop-owned sandbox.
func (s *Server) deleteSandbox(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
	entry, revision, ok := s.index.Next(id)
	if !ok {
		writeError(w, http.StatusNotFound, "SANDBOX_NOT_FOUND", "this Node does not own that sandbox", false)
		return
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
	if err := s.index.Commit(id, revision); err != nil {
		writeError(w, http.StatusInternalServerError, "SANDBOX_INDEX_FAILED", err.Error(), true)
		return
	}
	if s.injections != nil {
		_ = s.injections.DeleteSandboxInjections(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/operations/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "OPERATION_ID_REQUIRED", "operation ID is required", false)
		return
	}
	operation, err := s.node.GetOperation(r.Context(), &nodev1.GetOperationRequest{OperationId: id})
	if err != nil {
		writeError(w, http.StatusNotFound, "OPERATION_NOT_FOUND", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) sandbox(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[1] != "app" {
		writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "sandbox route not found", false)
		return
	}
	link, err := s.node.CreateSandboxAppLink(r.Context(), &nodev1.CreateSandboxAppLinkRequest{SandboxId: parts[0]})
	if err != nil {
		writeError(w, http.StatusBadGateway, "SANDBOX_APP_UNAVAILABLE", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, link)
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
	writeJSON(w, code, map[string]any{"error": errorCode, "message": message, "retryable": retryable})
}
