package mcpmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/injection"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresCustomMCPIsolationAndRevocation(t *testing.T) {
	database := os.Getenv("ISODO_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires disposable migrated PostgreSQL")
	}
	pool, err := pgxpool.New(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenant, other, sandbox := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{tenant, other} {
		if _, err = pool.Exec(t.Context(), `INSERT INTO tenants(id) VALUES($1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		ids := []string{tenant, other}
		pool.Exec(context.Background(), `DELETE FROM mcp_custom_servers WHERE tenant_id=ANY($1::uuid[])`, ids)
		pool.Exec(context.Background(), `DELETE FROM sandboxes WHERE tenant_id=ANY($1::uuid[])`, ids)
		pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=ANY($1::uuid[])`, ids)
	}()
	if _, err = pool.Exec(t.Context(), `INSERT INTO sandboxes(id,tenant_id,name,desired_state,observed_state,ownership_generation,revision,node_id) VALUES($1,$2,'custom-mcp-isolated','running','running',1,1,'mcp-test-node')`, sandbox, tenant); err != nil {
		t.Fatal(err)
	}
	sealer, _ := injection.NewSealer(bytes.Repeat([]byte{8}, 32))
	runtime := &fixtureRuntime{}
	h := New(pool, sealer, Config{}, runtime)
	const secret = "fixture-custom-bearer"
	var blockCall atomic.Bool
	started := make(chan struct{}, 1)
	h.customHTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		want := ""
		if r.URL.Path == "/private" {
			want = "Bearer " + secret
		}
		if r.Header.Get("Authorization") != want {
			t.Error("upstream credential mismatch")
		}
		var msg struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&msg)
		switch msg.Method {
		case "initialize":
			return response(200, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, msg.ID)), nil
		case "notifications/initialized":
			return response(202, ""), nil
		case "tools/list":
			return response(200, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"custom_echo","inputSchema":{}}]}}`, msg.ID)), nil
		case "tools/call":
			if blockCall.Load() {
				select {
				case started <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			return response(200, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"echo %s"}]}}`, msg.ID, secret)), nil
		}
		return response(204, ""), nil
	})}
	request := func(actor, credential, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer fixture-session")
		out := httptest.NewRecorder()
		h.Wrap(http.NotFoundHandler(), fixedAuth{actor, credential}).ServeHTTP(out, req)
		return out
	}
	require := func(out *httptest.ResponseRecorder, want int) {
		t.Helper()
		if out.Code != want {
			t.Fatalf("HTTP %d wanted %d: %s", out.Code, want, out.Body)
		}
		if strings.Contains(out.Body.String(), secret) {
			t.Fatal("custom credential leaked")
		}
	}
	input := map[string]any{"name": "Private test", "endpoint": "https://custom.example/private", "auth_kind": "bearer", "token": secret}
	require(request(tenant, "api_key", "POST", "/v1/mcp/custom", input), 403)
	bad := map[string]any{"name": "SSRF", "endpoint": "https://10.210.10.32/mcp", "auth_kind": "none"}
	require(request(tenant, "session", "POST", "/v1/mcp/custom", bad), 400)
	created := request(tenant, "session", "POST", "/v1/mcp/custom", input)
	require(created, 201)
	var entry struct {
		ID     string `json:"custom_server_id"`
		Market string `json:"market_mcp_id"`
	}
	if json.Unmarshal(created.Body.Bytes(), &entry) != nil || entry.ID == "" {
		t.Fatal("missing custom identity")
	}
	var cipher []byte
	if pool.QueryRow(t.Context(), `SELECT ciphertext FROM mcp_custom_servers WHERE id=$1`, entry.ID).Scan(&cipher) != nil || bytes.Contains(cipher, []byte(secret)) {
		t.Fatal("token stored in plaintext")
	}
	require(request(other, "session", "GET", "/v1/mcp/catalog", nil), 200)
	if strings.Contains(request(other, "session", "GET", "/v1/mcp/catalog", nil).Body.String(), entry.ID) {
		t.Fatal("private server crossed accounts")
	}
	require(request(other, "session", "PUT", "/v1/sandboxes/"+sandbox+"/mcp/"+entry.Market, nil), 404)
	require(request(tenant, "session", "GET", "/v1/mcp/catalog/"+entry.Market, nil), 200)
	require(request(other, "session", "GET", "/v1/mcp/catalog/"+entry.Market, nil), 404)
	t.Log("私有配置、加密与账号隔离通过；Sandbox ID:", sandbox)
	require(request(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/mcp/"+entry.Market, nil), 200)
	if runtime.request == nil || len(runtime.request.Bindings) != 1 || runtime.request.Bindings[0].MarketMcpId != entry.Market {
		t.Fatal("custom binding not sent over runtime RPC")
	}
	binding := runtime.request.Bindings[0]
	packed, _ := json.Marshal(runtime.request)
	if bytes.Contains(packed, []byte(secret)) {
		t.Fatal("token sent to Node")
	}
	broker := func(method, tool string, generation string) *httptest.ResponseRecorder {
		body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"name": tool, "arguments": map[string]any{}}}
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/internal/mcp/"+binding.BindingId, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+binding.Capability)
		req.Header.Set("X-Isodo-Sandbox", sandbox)
		req.Header.Set("X-Isodo-Node", "mcp-test-node")
		req.Header.Set("X-Isodo-Generation", generation)
		req.Header.Set("X-Isodo-Revision", "1")
		out := httptest.NewRecorder()
		h.Wrap(http.NotFoundHandler(), fixedAuth{}).ServeHTTP(out, req)
		return out
	}
	require(broker("tools/list", "", "1"), 200)
	require(broker("tools/call", "custom_echo", "1"), 200)
	require(broker("tools/call", "unapproved_tool", "1"), 403)
	require(broker("tools/call", "custom_echo", "99"), 403)
	t.Log("工具发现、调用、未知工具与旧身份拒绝通过；Sandbox ID:", sandbox)
	if _, err = pool.Exec(t.Context(), `UPDATE sandboxes SET observed_state='stopped' WHERE id=$1`, sandbox); err != nil {
		t.Fatal(err)
	}
	require(broker("tools/call", "custom_echo", "1"), 403)
	pool.Exec(t.Context(), `UPDATE sandboxes SET observed_state='running' WHERE id=$1`, sandbox)
	require(request(other, "session", "DELETE", "/v1/mcp/custom/"+entry.ID, nil), 204)
	require(broker("tools/call", "custom_echo", "1"), 200)
	blockCall.Store(true)
	inFlight := make(chan *httptest.ResponseRecorder, 1)
	go func() { inFlight <- broker("tools/call", "custom_echo", "1") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("custom fixture never started")
	}
	require(request(tenant, "session", "DELETE", "/v1/mcp/custom/"+entry.ID, nil), 204)
	select {
	case result := <-inFlight:
		require(result, 403)
	case <-time.After(3 * time.Second):
		t.Fatal("custom deletion did not cancel in-flight call")
	}
	blockCall.Store(false)
	require(broker("tools/call", "custom_echo", "1"), 403)
	public := request(tenant, "session", "POST", "/v1/mcp/custom", map[string]any{"name": "No auth", "endpoint": "https://custom.example/public", "auth_kind": "none"})
	require(public, 201)
	t.Log("无认证、停止拒绝、删除立即撤销通过；Sandbox ID:", sandbox, "（真实数据库；上游与 Node 为 fixture，不是真实 CHV 验收）")
}
