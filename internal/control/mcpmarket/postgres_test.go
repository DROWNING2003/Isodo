package mcpmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/injection"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixedAuth struct{ tenant, credential string }

func (a fixedAuth) Authenticate(context.Context, string) (auth.Identity, error) {
	return auth.Identity{TenantID: a.tenant, Credential: a.credential, Scopes: []string{"sandboxes:read", "sandboxes:write"}}, nil
}

type fixtureRuntime struct{ request *nodev1.SyncMCPRequest }

func (f *fixtureRuntime) NodeID() string { return "mcp-test-node" }
func (f *fixtureRuntime) SyncMCP(_ context.Context, r *nodev1.SyncMCPRequest) (*nodev1.SyncMCPResponse, error) {
	f.request = r
	return &nodev1.SyncMCPResponse{RestartRequired: true}, nil
}
func TestPostgresMCPAccountOAuthBindingLifecycle(t *testing.T) {
	database := os.Getenv("ISODO_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires disposable migrated PostgreSQL via scripts/test-mcp-market-postgres.sh")
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
		pool.Exec(context.Background(), `DELETE FROM mcp_sandbox_bindings WHERE tenant_id=ANY($1::uuid[])`, []string{tenant, other})
		pool.Exec(context.Background(), `DELETE FROM sandboxes WHERE tenant_id=ANY($1::uuid[])`, []string{tenant, other})
		for _, table := range []string{"mcp_oauth_transactions", "mcp_oauth_connections", "mcp_favorites", "tenants"} {
			column := "tenant_id"
			if table == "tenants" {
				column = "id"
			}
			pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE `+column+`=ANY($1::uuid[])`, []string{tenant, other})
		}
	}()
	if _, err = pool.Exec(t.Context(), `INSERT INTO sandboxes(id,tenant_id,name,desired_state,observed_state,ownership_generation,revision,node_id) VALUES($1,$2,'mcp-isolated-test','running','running',1,1,'mcp-test-node')`, sandbox, tenant); err != nil {
		t.Fatal(err)
	}
	sealer, _ := injection.NewSealer(bytes.Repeat([]byte{9}, 32))
	runtime := &fixtureRuntime{}
	h := New(pool, sealer, Config{"fixture-client", "fixture-client-secret", "https://api.drowning.top:18443/v1/mcp/oauth/callback"}, runtime)
	var refreshes atomic.Int32
	var blockCall atomic.Bool
	callStarted := make(chan struct{}, 1)
	realDiscovery := (&Handler{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.githubcopilot.com" && r.URL.Path == "/mcp/readonly" && r.Header.Get("Authorization") == "" {
			v := response(401, "")
			v.Header.Set("WWW-Authenticate", `Bearer resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly"`)
			return v, nil
		}
		if strings.Contains(r.URL.Path, "oauth-protected-resource") {
			return response(200, `{"resource":"https://api.githubcopilot.com/mcp/readonly","authorization_servers":["https://github.com/login/oauth"]}`), nil
		}
		return response(200, `{"issuer":"https://github.com/login/oauth","authorization_endpoint":"https://github.com/login/oauth/authorize","token_endpoint":"https://github.com/login/oauth/access_token","code_challenge_methods_supported":["S256"]}`), nil
	})}}).http
	h.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/login/oauth/access_token" {
			r.ParseForm()
			if r.Form.Get("code") == "bad-code" {
				return response(400, `{"error":"bad verification code fixture-client-secret"}`), nil
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
			}
			if r.Form.Get("client_id") != "fixture-client" || r.Form.Get("client_secret") != "fixture-client-secret" {
				t.Error("missing registered client")
			}
			return response(200, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","token_type":"bearer","expires_in":3600}`), nil
		}
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fixture-access") {
			if r.Method == "DELETE" {
				return response(204, ""), nil
			}
			var m struct {
				Method string `json:"method"`
				ID     int    `json:"id"`
			}
			json.NewDecoder(r.Body).Decode(&m)
			switch m.Method {
			case "initialize":
				return response(200, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, m.ID)), nil
			case "notifications/initialized":
				return response(202, ""), nil
			case "tools/list":
				return response(200, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"get_me","inputSchema":{}},{"name":"create_issue","inputSchema":{}}]}}`, m.ID)), nil
			case "tools/call":
				if blockCall.Load() {
					callStarted <- struct{}{}
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return response(200, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"fixture-readonly-result"}]}}`, m.ID)), nil
			}
		}
		return realDiscovery.Transport.RoundTrip(r)
	})}
	request := func(actor, credential, method, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if strings.HasPrefix(path, "/v1/mcp/oauth/callback") {
			req.Host = "api.drowning.top:18443"
		}
		req.Header.Set("Authorization", "Bearer fixture-session")
		result := httptest.NewRecorder()
		h.Wrap(http.NotFoundHandler(), fixedAuth{actor, credential}).ServeHTTP(result, req)
		return result
	}
	require := func(result *httptest.ResponseRecorder, status int) {
		t.Helper()
		if result.Code != status {
			t.Fatalf("HTTP %d wanted %d: %s", result.Code, status, result.Body)
		}
		for _, secret := range []string{"fixture-client-secret", "fixture-access", "fixture-refresh"} {
			if strings.Contains(result.Body.String(), secret) {
				t.Fatal("credential leaked")
			}
		}
	}
	t.Log("账号收藏、跨客户端持久化及账号隔离；Sandbox ID:", sandbox)
	require(request(tenant, "session", "PUT", "/v1/mcp/favorites/github"), 204)
	h2 := New(pool, sealer, h.config, runtime)
	out := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/v1/mcp/favorites", nil)
	req2.Header.Set("Authorization", "Bearer fixture-session")
	auth.Middleware(fixedAuth{tenant, "session"}, http.HandlerFunc(h2.Account)).ServeHTTP(out, req2)
	require(out, 200)
	if !strings.Contains(out.Body.String(), "GitHub") {
		t.Fatal("favorite not persisted")
	}
	out = request(other, "session", "GET", "/v1/mcp/favorites")
	require(out, 200)
	if strings.Contains(out.Body.String(), "GitHub") {
		t.Fatal("cross-tenant favorite")
	}
	require(request(tenant, "api_key", "POST", "/v1/mcp/connections/github/authorize"), 403)
	t.Log("PKCE 授权事务、一次性回调、加密令牌；Sandbox ID:", sandbox)
	// Cancel, timeout, state and bad-code paths never create a usable grant.
	begin := func() (string, string) {
		start := request(tenant, "session", "POST", "/v1/mcp/connections/github/authorize")
		require(start, 200)
		var result struct {
			URL string `json:"authorization_url"`
			ID  string `json:"transaction_id"`
		}
		json.Unmarshal(start.Body.Bytes(), &result)
		u, _ := url.Parse(result.URL)
		return result.ID, "/v1/mcp/oauth/callback?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=fixture-code"
	}
	cancelled, cb := begin()
	require(request(other, "session", "GET", "/v1/mcp/oauth/transactions/"+cancelled), 404)
	require(request(tenant, "session", "DELETE", "/v1/mcp/oauth/transactions/"+cancelled), 204)
	require(request(tenant, "session", "GET", cb), 400)
	timed, cb := begin()
	pool.Exec(t.Context(), `UPDATE mcp_oauth_transactions SET expires_at=now()-interval '1 minute' WHERE transaction_id=$1`, timed)
	require(request(tenant, "session", "GET", cb), 400)
	_, cb = begin()
	require(request(tenant, "session", "GET", strings.Replace(cb, "fixture-code", "bad-code", 1)), 400)
	require(request(tenant, "session", "GET", "/v1/mcp/oauth/callback?state="+strings.Repeat("x", 43)+"&code=fixture-code"), 400)

	start := request(tenant, "session", "POST", "/v1/mcp/connections/github/authorize")
	require(start, 200)
	var started struct {
		URL string `json:"authorization_url"`
		ID  string `json:"transaction_id"`
	}
	json.Unmarshal(start.Body.Bytes(), &started)
	u, _ := url.Parse(started.URL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 {
		t.Fatal("PKCE absent")
	}
	callback := "/v1/mcp/oauth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=fixture-code&iss=" + url.QueryEscape(issuer)
	out = request(tenant, "session", "GET", callback)
	require(out, 200)
	require(request(tenant, "session", "GET", callback), 400)
	var ciphertext []byte
	if err = pool.QueryRow(t.Context(), `SELECT ciphertext FROM mcp_oauth_connections WHERE tenant_id=$1`, tenant).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("fixture-access")) {
		t.Fatal("token not encrypted")
	}
	require(request(tenant, "session", "DELETE", "/v1/mcp/favorites/github"), 204)
	out = request(tenant, "session", "GET", "/v1/mcp/connections/github")
	require(out, 200)
	if !strings.Contains(out.Body.String(), "connected") {
		t.Fatal("unfavorite revoked OAuth")
	}
	t.Log("并发刷新串行化；Sandbox ID:", sandbox)
	expired := tokenMaterial{Access: "fixture-access", Refresh: "fixture-refresh", Type: "bearer", ExpiresAt: time.Now().Add(-time.Minute)}
	raw, _ := json.Marshal(expired)
	nonce, ciphertext, _ := sealer.Seal(raw)
	pool.Exec(t.Context(), `UPDATE mcp_oauth_connections SET nonce=$2,ciphertext=$3 WHERE tenant_id=$1`, tenant, nonce, ciphertext)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := h.freshToken(t.Context(), tenant); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refresh rotation count %d", refreshes.Load())
	}
	t.Log("显式绑定与 Broker 只读校验；Sandbox ID:", sandbox)
	require(request(other, "session", "PUT", "/v1/sandboxes/"+sandbox+"/mcp/github"), 404)
	require(request(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/mcp/github"), 200)
	if runtime.request == nil || len(runtime.request.Bindings) != 1 {
		t.Fatal("not synchronized")
	}
	b := runtime.request.Bindings[0]
	broker := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/internal/mcp/"+b.BindingId, strings.NewReader(body))
		r.SetPathValue("bindingID", b.BindingId)
		r.Header.Set("Authorization", "Bearer "+b.Capability)
		r.Header.Set("X-Isodo-Sandbox", sandbox)
		r.Header.Set("X-Isodo-Node", "mcp-test-node")
		r.Header.Set("X-Isodo-Generation", "1")
		r.Header.Set("X-Isodo-Revision", "1")
		w := httptest.NewRecorder()
		h.Broker(w, r)
		return w
	}
	require(broker(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_me"}}`), 200)
	require(broker(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_issue"}}`), 403)
	require(request(tenant, "session", "DELETE", "/v1/sandboxes/"+sandbox+"/mcp/github"), 200)
	require(broker(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`), 403)
	require(request(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/mcp/github"), 200)
	b = runtime.request.Bindings[0]
	blockCall.Store(true)
	streamResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		streamResult <- broker(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get_me"}}`)
	}()
	select {
	case <-callStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream fixture did not start")
	}
	require(request(tenant, "session", "DELETE", "/v1/mcp/connections/github"), 204)
	select {
	case result := <-streamResult:
		if result.Code == 200 {
			t.Fatal("disconnected in-flight call succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel active request")
	}
	blockCall.Store(false)
	_, cb = begin()
	require(request(tenant, "session", "GET", cb), 200)
	// A completed reconnect does not resurrect an old connection version.
	oldRequest := httptest.NewRequest("POST", "/internal/mcp/"+b.BindingId, nil)
	oldRequest.SetPathValue("bindingID", b.BindingId)
	oldRequest.Header.Set("Authorization", "Bearer "+b.Capability)
	oldRequest.Header.Set("X-Isodo-Sandbox", sandbox)
	oldRequest.Header.Set("X-Isodo-Node", "mcp-test-node")
	oldRequest.Header.Set("X-Isodo-Generation", "1")
	oldRequest.Header.Set("X-Isodo-Revision", "1")
	if _, err := h.authorized(t.Context(), oldRequest, 1); err == nil {
		t.Fatal("old grant version accepted after reconnect")
	}

	t.Log("Stop/Start 保留绑定，删除清理绑定并保留账号授权；Sandbox ID:", sandbox)
	pool.Exec(t.Context(), `UPDATE sandboxes SET desired_state='stopped',observed_state='stopped',revision=2 WHERE id=$1`, sandbox)
	var count int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM mcp_sandbox_bindings WHERE sandbox_id=$1`, sandbox).Scan(&count)
	if count != 1 {
		t.Fatal("stop lost binding")
	}
	pool.Exec(t.Context(), `UPDATE sandboxes SET desired_state='deleted',deletion_requested_at=now() WHERE id=$1`, sandbox)
	pool.QueryRow(t.Context(), `SELECT count(*) FROM mcp_sandbox_bindings WHERE sandbox_id=$1`, sandbox).Scan(&count)
	if count != 0 {
		t.Fatal("delete retained binding")
	}
	require(request(tenant, "session", "GET", "/v1/mcp/connections/github"), 200)
	require(request(tenant, "session", "DELETE", "/v1/mcp/connections/github"), 204)
	require(request(tenant, "session", "PUT", "/v1/mcp/favorites/github"), 204)
	out = request(tenant, "session", "GET", "/v1/mcp/favorites")
	require(out, 200)
	if !strings.Contains(out.Body.String(), "GitHub") {
		t.Fatal("disconnect removed favorite")
	}
	t.Log("专用 Sandbox 行与账号资源将在 deferred cleanup 删除；未调用真实 GitHub 或 CHV", sandbox)
}
