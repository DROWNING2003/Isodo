package mcpmarket

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/DROWNING2003/Isodo/internal/mcpclient"
)

var ErrOAuth = errors.New("GitHub 授权失败，请重新连接")
var ErrConfiguration = errors.New("尚未配置 GitHub OAuth App 和 HTTPS 回调")

const issuer = "https://github.com/login/oauth"

var challengePattern = regexp.MustCompile(`resource_metadata="([^"]+)"`)

type Config struct{ ClientID, ClientSecret, RedirectURI string }
type metadata struct {
	Issuer        string   `json:"issuer"`
	Authorization string   `json:"authorization_endpoint"`
	Token         string   `json:"token_endpoint"`
	Methods       []string `json:"code_challenge_methods_supported"`
}
type oauthPending struct {
	Verifier          string   `json:"verifier"`
	RedirectURI       string   `json:"redirect_uri"`
	Metadata          metadata `json:"metadata"`
	ConnectionVersion int64    `json:"connection_version"`
}
type tokenMaterial struct {
	Version          int64     `json:"-"`
	Access           string    `json:"access_token"`
	Refresh          string    `json:"refresh_token,omitempty"`
	Type             string    `json:"token_type"`
	ExpiresIn        int64     `json:"expires_in"`
	RefreshExpiresIn int64     `json:"refresh_token_expires_in"`
	Error            string    `json:"error,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitzero"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitzero"`
}

func randomValue() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func hash(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }
func configured(c Config) bool {
	u, err := url.Parse(c.RedirectURI)
	return c.ClientID != "" && c.ClientSecret != "" && err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "/v1/mcp/oauth/callback" && u.RawQuery == "" && u.Fragment == ""
}
func trustedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Host == "api.githubcopilot.com" {
		return u.Path == "/mcp/readonly" || u.Path == "/.well-known/oauth-protected-resource/mcp/readonly"
	}
	return u.Host == "github.com" && (u.Path == "/.well-known/oauth-authorization-server/login/oauth" || u.Path == "/.well-known/openid-configuration/login/oauth" || u.Path == "/login/oauth/.well-known/openid-configuration" || u.Path == "/login/oauth/authorize" || u.Path == "/login/oauth/access_token")
}
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrOAuth
		}
		if !slices.Contains([]string{"github.com", "api.githubcopilot.com"}, host) || port != "443" {
			return nil, ErrOAuth
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrOAuth
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("198.18.0.0/15").Contains(ip) {
				return nil, ErrOAuth
			}
		}
		var last error
		d := net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range ips {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}}}
}
func (h *Handler) discover(ctx context.Context) (metadata, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", mcpclient.Endpoint, nil)
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := h.http.Do(req)
	if err != nil {
		return metadata{}, ErrOAuth
	}
	resp.Body.Close()
	match := challengePattern.FindStringSubmatch(resp.Header.Get("WWW-Authenticate"))
	if resp.StatusCode != 401 || len(match) != 2 || !trustedURL(match[1]) {
		return metadata{}, ErrOAuth
	}
	var resource struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	if h.getJSON(ctx, match[1], &resource) != nil || resource.Resource != mcpclient.Endpoint || !slices.Contains(resource.Servers, issuer) {
		return metadata{}, ErrOAuth
	}
	for _, endpoint := range []string{"https://github.com/.well-known/oauth-authorization-server/login/oauth", "https://github.com/.well-known/openid-configuration/login/oauth", "https://github.com/login/oauth/.well-known/openid-configuration"} {
		var m metadata
		if h.getJSON(ctx, endpoint, &m) == nil && m.Issuer == issuer && m.Authorization == issuer+"/authorize" && m.Token == issuer+"/access_token" && slices.Contains(m.Methods, "S256") {
			return m, nil
		}
	}
	return metadata{}, ErrOAuth
}
func (h *Handler) getJSON(ctx context.Context, endpoint string, v any) error {
	if !trustedURL(endpoint) {
		return ErrOAuth
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, err := h.http.Do(req)
	if err != nil {
		return ErrOAuth
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrOAuth
	}
	return decodeBounded(resp.Body, v)
}
func decodeBounded(r io.Reader, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r, 1<<20+1))
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, v) != nil {
		return ErrOAuth
	}
	return nil
}
func (h *Handler) exchange(ctx context.Context, form url.Values) (tokenMaterial, error) {
	form.Set("client_id", h.config.ClientID)
	form.Set("client_secret", h.config.ClientSecret)
	req, _ := http.NewRequestWithContext(ctx, "POST", issuer+"/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return tokenMaterial{}, ErrOAuth
	}
	defer resp.Body.Close()
	var token tokenMaterial
	if resp.StatusCode != 200 || decodeBounded(resp.Body, &token) != nil || token.Error != "" || token.Access == "" || !strings.EqualFold(token.Type, "bearer") || token.ExpiresIn < 0 || token.RefreshExpiresIn < 0 {
		return tokenMaterial{}, ErrOAuth
	}
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(min(token.ExpiresIn, 31536000)) * time.Second)
	}
	if token.RefreshExpiresIn > 0 {
		token.RefreshExpiresAt = time.Now().Add(time.Duration(min(token.RefreshExpiresIn, 31536000)) * time.Second)
	}
	return token, nil
}
