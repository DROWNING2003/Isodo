package injection

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testCA(t *testing.T) *CA {
	t.Helper()
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestProxyInjectsHeadersAndTunnelsOtherHosts(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Authorization", r.Header.Get("Authorization"))
		w.Header().Set("X-Seen-Custom", r.Header.Get("X-Custom"))
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	ca := testCA(t)
	proxy, err := NewProxy("sandbox-test", ca, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	proxy.SetUpstreamRootCAs(pool)

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.ensureRulesForTest([]Rule{{
		Key: "one-off:test", Name: "test", Kind: KindOneOff,
		Spec:   Spec{Type: TypeHTTP, BaseURL: "https://" + upstreamURL.Host, HeaderName: "Authorization"},
		Secret: Secret{Headers: map[string]string{"Authorization": "Bearer injected"}},
	}}); err != nil {
		t.Fatal(err)
	}

	proxyURL, err := url.Parse(proxy.URL("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{
			RootCAs:    certPoolWith(ca.PEM()),
			MinVersion: tls.VersionTLS12,
		},
	}}
	response, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if got := response.Header.Get("X-Seen-Authorization"); got != "Bearer injected" {
		t.Fatalf("upstream authorization = %q", got)
	}
	stats := proxy.Stats()
	if stats.Injected != 1 || stats.LastHost != upstreamURL.Hostname() {
		t.Fatalf("stats = %+v", stats)
	}
}

func certPoolWith(pem []byte) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	return pool
}

// ensureRulesForTest validates through the same path as Manager.SetRules.
func (p *Proxy) ensureRulesForTest(rules []Rule) error {
	for index := range rules {
		rule, err := Normalize(rules[index])
		if err != nil {
			return err
		}
		rules[index] = rule
	}
	p.SetRules(rules)
	return nil
}

func TestRuleMatchingConditions(t *testing.T) {
	rule := Rule{Spec: Spec{
		Type: TypeOpenAI, BaseURL: "https://api.openai.com/v1/*",
		IfHeaders: map[string]string{"X-Scope": "prod"},
		IfQueries: map[string]string{"inject": "true"},
	}}
	request, _ := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions?inject=true", strings.NewReader("{}"))
	request.Header.Set("X-Scope", "prod")
	if !matchRule(request, "api.openai.com", rule) {
		t.Fatal("rule should match")
	}
	request.Header.Set("X-Scope", "dev")
	if matchRule(request, "api.openai.com", rule) {
		t.Fatal("rule should not match changed condition")
	}
	request.Header.Set("X-Scope", "prod")
	request.URL.RawQuery = "inject=false"
	if matchRule(request, "api.openai.com", rule) {
		t.Fatal("rule should not match changed query")
	}
}

func TestProxyInjectsPlainHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen", r.Header.Get("X-Plain"))
		_, _ = io.WriteString(w, "plain")
	}))
	defer upstream.Close()
	ca := testCA(t)
	proxy, err := NewProxy("sandbox-plain", ca, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	if err := proxy.ensureRulesForTest([]Rule{{
		Key: "one-off:plain", Name: "plain", Kind: KindOneOff,
		Spec:   Spec{Type: TypeHTTP, BaseURL: upstream.URL},
		Secret: Secret{Headers: map[string]string{"X-Plain": "yes"}},
	}}); err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse(proxy.URL("127.0.0.1"))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get("http://" + upstreamURL.Host + "/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("X-Seen"); got != "yes" {
		t.Fatalf("injected header = %q", got)
	}
}

func TestProxyRejectsOtherSandboxCapability(t *testing.T) {
	ca := testCA(t)
	first, err := NewProxy("first", ca, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewProxy("second", ca, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other, _ := url.Parse(second.URL("127.0.0.1"))
	own, _ := url.Parse(first.URL("127.0.0.1"))
	own.User = other.User
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(own)}}
	response, err := client.Get("http://example.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestHTTPSRuleRejectsDowngradeAndOtherPorts(t *testing.T) {
	rule := Rule{Spec: Spec{Type: TypeHTTP, BaseURL: "https://api.example.com/v1"}}
	for _, target := range []string{"http://api.example.com/v1", "https://api.example.com:8443/v1", "https://api.example.com/v1/private"} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if matchRule(req, "api.example.com", rule) {
			t.Fatalf("unexpected credential match: %s", target)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com:443/v1", nil)
	if !matchRule(req, "api.example.com", rule) {
		t.Fatal("explicit standard port should match")
	}
}
