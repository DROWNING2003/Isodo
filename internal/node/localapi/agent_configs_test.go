package localapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
)

type fakeAgentWriter struct {
	result                   AgentConfigWriteResult
	sandboxID, path, content string
}

func (f *fakeAgentWriter) WriteAgentConfig(_ context.Context, sandboxID, path string, content []byte) (AgentConfigWriteResult, error) {
	f.sandboxID, f.path, f.content = sandboxID, path, string(content)
	return f.result, nil
}

func TestPutAgentConfigWritesRedactedContent(t *testing.T) {
	server, err := New(fakeNode{}, "secret", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeAgentWriter{result: AgentConfigWriteResult{Path: "~/.codex/config.toml", Sha256: "digest", Bytes: 10, RequiresRestart: true}}
	server.SetAgentConfigWriter(writer)
	request := httptest.NewRequest(http.MethodPut, "/sandboxes/sandbox-1/agent-config", strings.NewReader(`{"path":"~/.codex/config.toml","content":"api_key = \"${ISODO_SECRET}\""}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if writer.sandboxID != "sandbox-1" || writer.path != "~/.codex/config.toml" || !strings.Contains(writer.content, "ISODO_SECRET") {
		t.Fatalf("write = %+v", writer)
	}
}

func TestPutAgentConfigRejectsUnsafePath(t *testing.T) {
	server, err := New(fakeNode{}, "secret", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.SetAgentConfigWriter(&fakeAgentWriter{})
	request := httptest.NewRequest(http.MethodPut, "/sandboxes/sandbox-1/agent-config", strings.NewReader(`{"path":"../../secret","content":"x"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

var _ nodev1.NodeOrchestratorServer = fakeNode{}
