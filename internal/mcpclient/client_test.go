package mcpclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamableSessionReadOnlyAndSSE(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing trusted authorization")
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		var m struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		calls++
		if calls > 1 && (r.Header.Get("Mcp-Session-Id") != "fixture-session" || r.Header.Get("MCP-Protocol-Version") != Protocol) {
			t.Error("session/version not carried")
		}
		switch m.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":%q}}`, m.ID, Protocol)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"get_me","inputSchema":{}},{"name":"create_issue","inputSchema":{}}]}}`, m.ID)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"fixture-result\"}]}}\n\n", m.ID)
		}
	}))
	defer server.Close()
	s := Session{Client: server.Client(), Endpoint: server.URL, Token: "fixture-token"}
	defer s.Close(t.Context())
	if err := s.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	tools, err := s.Tools(t.Context())
	if err != nil || len(tools) != 1 || tools[0].Name != "get_me" {
		t.Fatalf("tools: %v %v", tools, err)
	}
	if _, err = s.Request(t.Context(), "tools/call", map[string]any{"name": "get_me"}); err != nil {
		t.Fatal(err)
	}
}
func TestNoCredentialRedirect(t *testing.T) {
	leaked := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer server.Close()
	s := Session{Client: server.Client(), Endpoint: server.URL, Token: "do-not-forward"}
	if s.Initialize(t.Context()) == nil || leaked {
		t.Fatal("redirect followed")
	}
	for _, name := range []string{"create_issue", "update_pull_request", "get_secret", "issue_write", "pull_request_review_write"} {
		if Allowed(name) {
			t.Fatalf("write tool allowed: %s", name)
		}
	}
}
