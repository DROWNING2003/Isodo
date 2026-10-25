package localapi

import (
	"context"
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
	request := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"sandbox_id":"我的沙箱"}`))
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
	request := httptest.NewRequest(http.MethodPost, "/sandboxes/sandbox-1/stop", nil)
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
