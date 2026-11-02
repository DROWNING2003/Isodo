package mcpmarket

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestGitHubDiscoveryPKCEAndStrictEndpoints(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.githubcopilot.com/mcp/readonly":
			resp := response(401, "")
			resp.Header.Set("WWW-Authenticate", `Bearer resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly"`)
			return resp, nil
		case "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly":
			return response(200, `{"resource":"https://api.githubcopilot.com/mcp/readonly","authorization_servers":["https://github.com/login/oauth"]}`), nil
		default:
			return response(200, `{"issuer":"https://github.com/login/oauth","authorization_endpoint":"https://github.com/login/oauth/authorize","token_endpoint":"https://github.com/login/oauth/access_token","code_challenge_methods_supported":["S256"]}`), nil
		}
	})}
	h := &Handler{http: client}
	meta, err := h.discover(t.Context())
	if err != nil || meta.Token != issuer+"/access_token" {
		t.Fatalf("discovery: %v %v", meta, err)
	}
	// RFC7636 Appendix B vector.
	if got := base64.RawURLEncoding.EncodeToString(hash("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal(got)
	}
	for _, raw := range []string{"http://github.com/login/oauth/access_token", "https://github.com.evil.test/login/oauth/access_token", "https://github.com@evil.test/login/oauth/access_token", "https://127.0.0.1/", "https://github.com:443/login/oauth/access_token", "https://github.com/login/oauth/access_token?evil=1", "https://api.githubcopilot.com/mcp/x/all"} {
		if trustedURL(raw) {
			t.Fatalf("unsafe endpoint accepted %s", raw)
		}
	}
	if configured(Config{"id", "secret", "http://api.drowning.top/v1/mcp/oauth/callback"}) {
		t.Fatal("insecure callback")
	}
}
func TestTokenExchangeNeverReflectsCodeOrSecret(t *testing.T) {
	h := &Handler{config: Config{ClientID: "registered-id", ClientSecret: "fixture-secret"}, http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != issuer+"/access_token" {
			t.Fatal("wrong token endpoint")
		}
		r.ParseForm()
		if r.Form.Get("client_id") != "registered-id" || r.Form.Get("code_verifier") != "fixture-verifier" {
			t.Error("pre-registered client/PKCE absent")
		}
		return response(400, `{"error":"fixture-secret fixture-code"}`), nil
	})}}
	_, err := h.exchange(context.Background(), map[string][]string{"code": {"fixture-code"}, "code_verifier": {"fixture-verifier"}})
	if err == nil || strings.Contains(err.Error(), "fixture-") {
		t.Fatalf("error leaks upstream: %v", err)
	}
}
