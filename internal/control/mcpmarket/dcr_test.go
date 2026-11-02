package mcpmarket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

type dcrFixture struct {
	provider                 string
	registrations, refreshes atomic.Int32
	bad                      string
}

func (f *dcrFixture) client() *http.Client {
	return &http.Client{Transport: roundTrip(f.roundTrip), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (f *dcrFixture) roundTrip(r *http.Request) (*http.Response, error) {
	origin := "https://mcp.notion.com"
	as := origin
	safe, unsafe := "notion-search", "notion-create-pages"

	if r.Method == "GET" && r.URL.Host == strings.TrimPrefix(origin, "https://") && r.URL.Path == "/mcp" {
		resp := response(401, "")
		resp.Header.Set("WWW-Authenticate", `Bearer resource_metadata="`+origin+`/.well-known/oauth-protected-resource/mcp"`)
		return resp, nil
	}
	if strings.Contains(r.URL.Path, "oauth-protected-resource") {
		resource := origin + "/mcp"
		if f.bad == "resource" {
			resource = "https://evil.test/mcp"
		}
		body, _ := json.Marshal(map[string]any{"resource": resource, "authorization_servers": []string{as}, "scopes_supported": []string{"default"}})
		return response(200, string(body)), nil
	}
	if strings.Contains(r.URL.Path, ".well-known") {
		issuer := as
		if f.bad == "issuer" {
			issuer = "https://evil.test"
		}
		registration := as + "/register"
		if f.bad == "no-dcr" {
			registration = ""
		}
		if f.bad == "ssrf" {
			registration = "https://127.0.0.1/register"
		}
		body, _ := json.Marshal(map[string]any{"issuer": issuer, "authorization_endpoint": as + "/authorize", "token_endpoint": as + "/token", "registration_endpoint": registration, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"}, "authorization_response_iss_parameter_supported": f.bad != "mixup", "scopes_supported": []string{"projects:read", "database:read"}})
		return response(200, string(body)), nil
	}
	if r.URL.Path == "/register" {
		f.registrations.Add(1)
		var registration struct {
			Redirects []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&registration)
		if f.bad == "register" {
			return response(400, `{"error":"invalid_redirect_uri"}`), nil
		}
		body, _ := json.Marshal(dynamicClient{ID: "fixture-client", Secret: "fixture-client-secret", Method: "client_secret_post", Redirects: registration.Redirects})
		return response(201, string(body)), nil
	}
	if r.URL.Path == "/token" {
		_ = r.ParseForm()
		if r.Form.Get("client_id") == "" {
			if id, _, ok := r.BasicAuth(); !ok || id == "" {
				return response(401, `{"error":"invalid_client"}`), nil
			}
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			f.refreshes.Add(1)
		}
		if f.bad == "invalid_grant" {
			return response(400, `{"error":"invalid_grant"}`), nil
		}
		if f.bad == "invalid_client" {
			return response(401, `{"error":"invalid_client"}`), nil
		}
		access := "fixture-access"
		if f.provider == "notion" {
			access = "fixture-user:fixture-grant:fixture-secret"
		}
		return response(200, `{"access_token":"`+access+`","refresh_token":"fixture-rotated","token_type":"Bearer","expires_in":3600,"workspace_id":"fixture-workspace","workspace_name":"Test workspace"}`), nil
	}
	if r.URL.Path == "/mcp" && r.Method == "POST" {

		var rpc struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		var result any = map[string]any{"content": []any{map[string]string{"type": "text", "text": "fixture read result"}}}
		switch rpc.Method {
		case "initialize":
			result = map[string]string{"protocolVersion": "2025-06-18"}
		case "notifications/initialized":
			return response(202, ""), nil
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": safe, "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": unsafe, "inputSchema": map[string]any{"type": "object"}}}}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
		return response(200, string(body)), nil
	}
	return response(404, ""), nil
}
func TestDCRDiscoveryPolicy(t *testing.T) {
	for _, provider := range []string{"notion"} {
		for _, bad := range []string{"", "issuer", "resource", "no-dcr", "ssrf", "mixup"} {
			t.Run(provider+"/"+bad, func(t *testing.T) {
				f := &dcrFixture{provider: provider, bad: bad}
				h := &Handler{http: f.client(), config: Config{RedirectURI: "https://api.drowning.top:18443/v1/mcp/oauth/callback"}}
				g, err := h.discoverProvider(t.Context(), provider)
				if (err == nil) != (bad == "") {
					t.Fatalf("discovery bad=%s err=%v", bad, err)
				}
				if err == nil && (g.Provider != provider || g.Metadata.Registration == "" || g.Metadata.Issuer == issuer) {
					t.Fatal("provider metadata not preserved")
				}
			})
		}
	}
}
func TestOfficialProviderPolicy(t *testing.T) {
	if !readonlyTool("notion", "notion-search") || readonlyTool("notion", "notion-create-pages") {
		t.Fatal("Notion policy incorrect")
	}
	if officialProvider("supabase") || oauthURL("supabase", "https://api.supabase.com") || readonlyTool("supabase", "list_tables") {
		t.Fatal("removed provider accepted")
	}
	if _, err := providerEndpoint("supabase"); err == nil {
		t.Fatal("removed endpoint accepted")
	}
}
func TestDCRClientAuthentication(t *testing.T) {
	for _, method := range []string{"none", "client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			g := grantContext{Provider: "notion", Resource: "https://mcp.notion.com/mcp", Redirect: "https://api.test/v1/mcp/oauth/callback", Metadata: metadata{Issuer: "https://mcp.notion.com", Authorization: "https://mcp.notion.com/authorize", Token: "https://mcp.notion.com/token"}, Client: dynamicClient{ID: "client", Secret: "secret", Method: method}}
			h := &Handler{config: Config{RedirectURI: g.Redirect}, http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				_ = r.ParseForm()
				_, _, basic := r.BasicAuth()
				if basic != (method == "client_secret_basic") || r.Form.Get("resource") != g.Resource || method == "none" && r.Form.Get("client_secret") != "" || method == "client_secret_post" && r.Form.Get("client_secret") != "secret" {
					t.Fatal("incorrect OAuth client authentication")
				}
				return response(200, `{"access_token":"fixture-user:fixture-grant:fixture-secret","token_type":"Bearer","expires_in":3600}`), nil
			})}}
			if _, err := h.exchangeGrant(context.Background(), url.Values{"grant_type": {"authorization_code"}, "code_verifier": {"verifier"}}, &g); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDCRJSONSecretRedaction(t *testing.T) {
	secret := "secret\"\\value"
	raw, _ := json.Marshal(map[string]any{secret: []any{map[string]string{"description": "prefix " + secret}}})
	clean, err := redactJSON(raw, secret)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(clean, &decoded) != nil {
		t.Fatal("redaction broke JSON")
	}
	if _, exists := decoded[secret]; exists || !strings.Contains(string(clean), "[REDACTED]") {
		t.Fatal("escaped secret leaked")
	}
}

func TestNotionOAuthAccessTokenFormat(t *testing.T) {
	for _, token := range []string{"user:grant:secret", "normal-token"} {
		if !validOAuthAccessToken("notion", token) {
			t.Fatal("Notion token rejected")
		}
	}
	for _, token := range []string{"", ":grant:secret", "user::secret", "user:grant:", "user:grant:secret:extra", "user:grant:secret\r\nX-Test:value", "user:grant:with space"} {
		if validOAuthAccessToken("notion", token) {
			t.Fatal("unsafe token accepted")
		}
	}
	for _, provider := range []string{"github", "supabase", "custom"} {
		if validOAuthAccessToken(provider, "user:grant:secret") {
			t.Fatal("Notion exception leaked to other provider")
		}
	}
}
