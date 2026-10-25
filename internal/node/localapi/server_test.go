package localapi

import (
	"context"
	"encoding/json"
	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeNode struct {
	nodev1.UnimplementedNodeOrchestratorServer
}

func (fakeNode) GetNodeStatus(context.Context, *emptypb.Empty) (*nodev1.NodeStatus, error) {
	return &nodev1.NodeStatus{NodeId: "local"}, nil
}

func TestHandlerRequiresLocalToken(t *testing.T) {
	server, err := New(fakeNode{}, "secret", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHandlerRejectsInvalidSandboxID(t *testing.T) {
	server, err := New(fakeNode{}, "secret", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(`{"sandbox_id":"我的沙箱"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestHandlerHealthWithLocalToken(t *testing.T) {
	server, err := New(fakeNode{}, "secret", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

type lifecycleNode struct {
	fakeNode
	request *nodev1.ApplyOperationRequest
}

func (n *lifecycleNode) ApplyOperation(_ context.Context, request *nodev1.ApplyOperationRequest) (*nodev1.ApplyOperationResponse, error) {
	n.request = request
	return &nodev1.ApplyOperationResponse{Operation: &nodev1.Operation{OperationId: request.OperationId, State: nodev1.OperationState_OPERATION_STATE_ACCEPTED}}, nil
}

func TestLocalSandboxStopPreservesInstance(t *testing.T) {
	index, err := NewSandboxIndex("")
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add("sandbox-1", "test"); err != nil {
		t.Fatal(err)
	}
	node := &lifecycleNode{}
	server, err := New(node, "secret", nil, nil, index)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/sandbox-1/stop", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	if node.request == nil || node.request.GetAction() != nodev1.OperationAction_OPERATION_ACTION_STOP {
		t.Fatalf("request = %+v", node.request)
	}
	if node.request.GetRevision() != 2 {
		t.Fatalf("revision = %d, want 2", node.request.GetRevision())
	}
	if entry, _, ok := index.Next("sandbox-1"); !ok || entry.Revision != 2 {
		t.Fatalf("index entry = %+v, ok=%v", entry, ok)
	}
}

type snapshotNode struct {
	lifecycleNode
	state nodev1.OperationState
}

func (n *snapshotNode) GetOperation(context.Context, *nodev1.GetOperationRequest) (*nodev1.Operation, error) {
	return &nodev1.Operation{State: n.state, ErrorCode: "STOP_FAILED", Message: "guest still running"}, nil
}
func (n *snapshotNode) InspectSandbox(context.Context, *nodev1.InspectSandboxRequest) (*nodev1.SandboxInspection, error) {
	return &nodev1.SandboxInspection{ObservedState: "running"}, nil
}

func TestCanonicalSnapshotAndPatchPreserveActionRevision(t *testing.T) {
	index, err := NewSandboxIndex("")
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add("local-a", "before"); err != nil {
		t.Fatal(err)
	}
	if err := index.SetAction("local-a", "op-a", "stop", nil); err != nil {
		t.Fatal(err)
	}
	node := &snapshotNode{state: nodev1.OperationState_OPERATION_STATE_FAILED}
	server, err := New(node, "secret", nil, nil, index)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/v1/sandboxes/local-a", strings.NewReader(`{"name":"after","metadata":{"purpose":"demo"}}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result publicapi.Sandbox
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Name != "after" || result.Revision == nil || *result.Revision != "1" || result.LastAction == nil || result.LastAction.Revision != "1" {
		t.Fatalf("patch changed lifecycle identity: %+v", result)
	}
	if result.ObservedState != "running" || result.LastAction.Status != "failed" || result.LastAction.Error.Code != "STOP_FAILED" {
		t.Fatalf("action failure was confused with VM state: %+v", result)
	}
}

func TestCanonicalListPaginationAndOldManagementRouteRemoved(t *testing.T) {
	index, err := NewSandboxIndex("")
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add("local-a", "A"); err != nil {
		t.Fatal(err)
	}
	server, err := New(fakeNode{}, "secret", nil, nil, index)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/v1/sandboxes?limit=1&offset=1", 200}, {"/v1/sandboxes?limit=101", 400}, {"/sandboxes", 404},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
		if test.status == 200 {
			var page publicapi.SandboxList
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || page.Limit != 1 || page.Offset != 1 || len(page.Items) != 0 {
				t.Fatalf("page=%+v", page)
			}
		}
	}
}

func TestOldAgentConfigRouteWasReplacedByFiles(t *testing.T) {
	server, err := New(fakeNode{}, "secret", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/sandboxes/local-a/agent-config", strings.NewReader(`{"path":"~/.codex/config.toml","content":"model = 'test'"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("old route status=%d", response.Code)
	}
}
