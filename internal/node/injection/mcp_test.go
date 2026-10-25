package injection

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This is a local protocol fixture, not a third-party MCP integration.
// The client knows only a placeholder; real material lives in the proxy.
func TestMCPStreamableHTTPThroughInjection(t *testing.T) {
	const secret = "test-only-mcp-secret"
	const placeholder = "isodo-mcp-placeholder"
	const session = "local-fixture-session"
	var calls atomic.Int64
	var initialized atomic.Bool
	releaseStream := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Accept") != "application/json, text/event-stream" {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&message) != nil {
			w.WriteHeader(400)
			return
		}
		if message.Method != "initialize" && (r.Header.Get("Mcp-Session-Id") != session || r.Header.Get("Mcp-Protocol-Version") != "2025-11-25") {
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch message.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", session)
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"isodo-test-mcp","version":"1"}}}`)
		case "notifications/initialized":
			initialized.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if !initialized.Load() {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"Local test tool","inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}}]}}`)
		case "tools/call":
			if !initialized.Load() {
				w.WriteHeader(400)
				return
			}
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Message string `json:"message"`
				} `json:"arguments"`
			}
			if json.Unmarshal(message.Params, &params) != nil || params.Name != "echo" || params.Arguments.Message != "mcp-ok" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"mcp-ok\"}],\"isError\":false}}\n\n")
			w.(http.Flusher).Flush()
			// Keep the stream open until the client has received its event. This
			// catches proxies that buffer the entire response before forwarding.
			select {
			case <-releaseStream:
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(400)
		}
	}))
	defer upstream.Close()
	defer close(releaseStream)
	ca := testCA(t)
	proxy, err := NewProxy("mcp-sandbox", ca, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	proxy.SetUpstreamRootCAs(roots)
	rule := Rule{Key: "mcp", Name: "Local MCP", Kind: KindOneOff, Spec: Spec{Type: TypeHTTP, BaseURL: upstream.URL + "/mcp", IfHeaders: map[string]string{"Authorization": "Bearer " + placeholder}}, Secret: Secret{Headers: map[string]string{"Authorization": "Bearer " + secret}}}
	if err := proxy.ensureRulesForTest([]Rule{rule}); err != nil {
		t.Fatal(err)
	}
	proxyURL, err := url.Parse(proxy.URL("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := certPoolWith(ca.PEM())
	clientRoots.AddCert(upstream.Certificate())
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: clientRoots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	post := func(body string, withSession bool) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+placeholder)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if withSession {
			req.Header.Set("Mcp-Session-Id", session)
			req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	read := func(response *http.Response) []byte {
		t.Helper()
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(secret)) {
			t.Fatal("secret leaked into client response")
		}
		return body
	}
	response := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"isodo-probe","version":"1"}}}`, false)
	if response.StatusCode != 200 || response.Header.Get("Mcp-Session-Id") != session {
		t.Fatal("MCP initialization/session header failed")
	}
	read(response)
	response = post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, true)
	if response.StatusCode != 202 {
		t.Fatal("initialized notification failed")
	}
	read(response)
	response = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, true)
	if response.StatusCode != 200 || !bytes.Contains(read(response), []byte(`"echo"`)) {
		t.Fatal("tools/list failed")
	}
	response = post(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"mcp-ok"}}}`, true)
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("tool call SSE failed")
	}
	scanner := bufio.NewScanner(response.Body)
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			if strings.Contains(line, secret) {
				t.Fatal("secret leaked in SSE")
			}
			found = strings.Contains(line, "mcp-ok")
			break
		}
	}
	if !found {
		t.Fatalf("streamed tool result missing: %v", scanner.Err())
	}
	response.Body.Close()
	if calls.Load() != 4 || proxy.Stats().Injected != 4 {
		t.Fatal("not all MCP requests passed through injection")
	}
	proxy.SetRules(nil)
	response = post(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, true)
	defer response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("revoked MCP still authorized: %d", response.StatusCode)
	}
	if calls.Load() != 4 {
		t.Fatalf("unauthorized tool reached handler: %d", calls.Load())
	}
}
