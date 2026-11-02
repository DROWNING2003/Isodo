package mcpmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/DROWNING2003/Isodo/internal/mcpclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type dynamicClient struct {
	ID            string   `json:"client_id"`
	Secret        string   `json:"client_secret,omitempty"`
	Method        string   `json:"token_endpoint_auth_method"`
	SecretExpires int64    `json:"client_secret_expires_at"`
	Redirects     []string `json:"redirect_uris,omitempty"`
}
type grantContext struct {
	Tenant   string        `json:"tenant_id,omitempty"`
	Provider string        `json:"provider"`
	Resource string        `json:"resource"`
	Redirect string        `json:"redirect_uri"`
	Scope    string        `json:"scope,omitempty"`
	Metadata metadata      `json:"metadata"`
	Client   dynamicClient `json:"client"`
}

var errRegistration = errors.New("MCP 服务不支持安全 DCR 或动态注册失败")
var errInvalidClient = errors.New("MCP 动态客户端已失效，请重新连接")

func callbackConfigured(c Config) bool {
	copy := c
	copy.ClientID = "configured"
	copy.ClientSecret = "configured"
	return configured(copy)
}
func (h *Handler) providerJSON(ctx context.Context, provider, endpoint string, out any) error {
	if !oauthURL(provider, endpoint) {
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
	return decodeBounded(resp.Body, out)
}
func (h *Handler) discoverProvider(ctx context.Context, provider string) (grantContext, error) {
	endpoint, err := providerEndpoint(provider)
	if err != nil {
		return grantContext{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := h.http.Do(req)
	if err != nil {
		return grantContext{}, ErrOAuth
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		return grantContext{}, ErrOAuth
	}
	base, _ := url.Parse(endpoint)
	base.RawQuery = ""
	origin := base.Scheme + "://" + base.Host
	candidates := []string{}
	for _, header := range resp.Header.Values("WWW-Authenticate") {
		if match := challengePattern.FindStringSubmatch(header); len(match) == 2 {
			candidates = append(candidates, match[1])
		}
	}
	if len(candidates) > 1 {
		return grantContext{}, ErrOAuth
	}
	if len(candidates) == 0 {
		candidates = []string{origin + "/.well-known/oauth-protected-resource" + base.Path, origin + "/.well-known/oauth-protected-resource"}
	}
	var resource struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	found := false
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, origin+"/") {
			return grantContext{}, ErrOAuth
		}
		if h.providerJSON(ctx, provider, candidate, &resource) == nil {
			found = true
			break
		}
	}
	if !found || (resource.Resource != base.String() && resource.Resource != origin) || len(resource.Servers) == 0 || len(resource.Servers) > 8 {
		return grantContext{}, ErrOAuth
	}
	scope := ""
	if provider == "notion" && slices.Contains(resource.Scopes, "default") {
		scope = "default"
	}
	// Header scopes are authoritative; parse the quoted parameter without allowing
	// arbitrary authorization query parameters from the resource server.
	for _, header := range resp.Header.Values("WWW-Authenticate") {
		if match := scopePattern.FindStringSubmatch(header); len(match) == 2 {
			scope = match[1]
		}
	}
	if len(scope) > 2048 || strings.ContainsAny(scope, "\r\n\x00") {
		return grantContext{}, ErrOAuth
	}
	for _, as := range resource.Servers {
		if !oauthURL(provider, as) {
			continue
		}
		u, _ := url.Parse(as)
		prefix := u.Scheme + "://" + u.Host
		p := strings.TrimRight(u.Path, "/")
		for _, path := range []string{"/.well-known/oauth-authorization-server" + p, "/.well-known/openid-configuration" + p, p + "/.well-known/openid-configuration"} {
			var m metadata
			if h.providerJSON(ctx, provider, prefix+path, &m) != nil {
				continue
			}
			if m.Issuer != as || !oauthURL(provider, m.Authorization) || !oauthURL(provider, m.Token) || !oauthURL(provider, m.Registration) || !slices.Contains(m.Methods, "S256") || !m.ResponseIssuer {
				continue
			}

			return grantContext{Provider: provider, Resource: resource.Resource, Redirect: h.config.RedirectURI, Scope: scope, Metadata: m}, nil
		}
	}
	return grantContext{}, errRegistration
}
func validRegistration(c dynamicClient, redirect string) bool {
	if c.ID == "" || len(c.ID) > 2048 || len(c.Secret) > 4096 || strings.ContainsAny(c.ID+c.Secret, "\r\n\x00") {
		return false
	}
	if !slices.Contains([]string{"none", "client_secret_basic", "client_secret_post"}, c.Method) || c.Method != "none" && c.Secret == "" {
		return false
	}
	if c.SecretExpires < 0 || c.SecretExpires > 0 && c.SecretExpires <= time.Now().Add(30*time.Second).Unix() {
		return false
	}
	return len(c.Redirects) == 0 || len(c.Redirects) == 1 && c.Redirects[0] == redirect
}
func (h *Handler) registerClient(ctx context.Context, g grantContext) (dynamicClient, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return dynamicClient{}, err
	}
	defer tx.Rollback(ctx)
	key := g.Provider + "\n" + g.Metadata.Issuer + "\n" + g.Redirect
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return dynamicClient{}, err
	}
	var nonce, sealed []byte
	err = tx.QueryRow(ctx, `SELECT nonce,ciphertext FROM mcp_oauth_registrations WHERE provider=$1 AND issuer=$2 AND redirect_uri=$3 AND status='active'`, g.Provider, g.Metadata.Issuer, g.Redirect).Scan(&nonce, &sealed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return dynamicClient{}, err
	}
	if err == nil {
		raw, e := h.sealer.Open(nonce, sealed)
		var saved grantContext
		if e != nil || json.Unmarshal(raw, &saved) != nil || saved.Provider != g.Provider || saved.Metadata.Issuer != g.Metadata.Issuer || saved.Redirect != g.Redirect {
			return dynamicClient{}, errRegistration
		}
		if validRegistration(saved.Client, g.Redirect) {
			return saved.Client, tx.Commit(ctx)
		}
	}
	method := ""
	for _, candidate := range []string{"none", "client_secret_basic", "client_secret_post"} {
		if slices.Contains(g.Metadata.AuthMethods, candidate) {
			method = candidate
			break
		}
	}
	if method == "" {
		return dynamicClient{}, errRegistration
	}
	body, _ := json.Marshal(map[string]any{"client_name": "Isodo MCP", "redirect_uris": []string{g.Redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": method})
	req, _ := http.NewRequestWithContext(ctx, "POST", g.Metadata.Registration, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return dynamicClient{}, errRegistration
	}
	defer resp.Body.Close()
	var client dynamicClient
	if resp.StatusCode != 201 && resp.StatusCode != 200 || decodeBounded(resp.Body, &client) != nil {
		return dynamicClient{}, errRegistration
	}
	if client.Method == "" {
		client.Method = "client_secret_basic"
	}
	if !validRegistration(client, g.Redirect) || !slices.Contains(g.Metadata.AuthMethods, client.Method) {
		return dynamicClient{}, errRegistration
	}
	g.Client = client
	raw, _ := json.Marshal(g)
	nonce, sealed, err = h.sealer.Seal(raw)
	if err != nil {
		return dynamicClient{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_registrations(provider,issuer,redirect_uri,nonce,ciphertext,client_id_hash,status) VALUES($1,$2,$3,$4,$5,$6,'active') ON CONFLICT(provider,issuer,redirect_uri) DO UPDATE SET nonce=EXCLUDED.nonce,ciphertext=EXCLUDED.ciphertext,client_id_hash=EXCLUDED.client_id_hash,status='active',updated_at=now()`, g.Provider, g.Metadata.Issuer, g.Redirect, nonce, sealed, hash(client.ID))
	if err != nil {
		return dynamicClient{}, err
	}
	return client, tx.Commit(ctx)
}
func (h *Handler) exchangeGrant(ctx context.Context, form url.Values, g *grantContext) (tokenMaterial, error) {
	if g == nil {
		return h.exchange(ctx, form)
	}
	if !officialProvider(g.Provider) || g.Provider == "github" || !oauthURL(g.Provider, g.Metadata.Token) || !oauthURL(g.Provider, g.Metadata.Issuer) || !oauthURL(g.Provider, g.Metadata.Authorization) || g.Redirect != h.config.RedirectURI || !validRegistration(g.Client, g.Redirect) {
		return tokenMaterial{}, ErrOAuth
	}
	form.Set("resource", g.Resource)
	if g.Client.Method != "client_secret_basic" {
		form.Set("client_id", g.Client.ID)
	}
	if g.Client.Method == "client_secret_post" {
		form.Set("client_secret", g.Client.Secret)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", g.Metadata.Token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if g.Client.Method == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(g.Client.ID), url.QueryEscape(g.Client.Secret))
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return tokenMaterial{}, ErrOAuth
	}
	defer resp.Body.Close()
	var token tokenMaterial
	if decodeBounded(resp.Body, &token) != nil {
		slog.Warn("MCP OAuth token response rejected", "provider", g.Provider, "status", resp.StatusCode, "stage", "decode")
		return tokenMaterial{}, ErrOAuth
	}
	if token.Error == "invalid_client" {

		return tokenMaterial{}, errInvalidClient
	}
	if resp.StatusCode != 200 || token.Error != "" || token.Access == "" || !strings.EqualFold(token.Type, "bearer") || token.ExpiresIn < 0 || token.RefreshExpiresIn < 0 || len(token.Access) > 16384 || len(token.Refresh) > 16384 || !validOAuthAccessToken(g.Provider, token.Access) || strings.ContainsAny(token.Refresh, "\r\n\x00") {
		slog.Warn("MCP OAuth token response rejected", "provider", g.Provider, "status", resp.StatusCode, "stage", "validation", "has_access", token.Access != "", "bearer_type", strings.EqualFold(token.Type, "bearer"), "has_oauth_error", token.Error != "", "negative_access_expiry", token.ExpiresIn < 0, "negative_refresh_expiry", token.RefreshExpiresIn < 0, "access_oversize", len(token.Access) > 16384, "refresh_oversize", len(token.Refresh) > 16384, "access_format_valid", validOAuthAccessToken(g.Provider, token.Access), "refresh_controls", strings.ContainsAny(token.Refresh, "\r\n\x00"))
		return tokenMaterial{}, ErrOAuth
	}
	token.Grant = g
	if token.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(min(token.ExpiresIn, 31536000)) * time.Second)
	}
	if token.RefreshExpiresIn > 0 {
		token.RefreshExpiresAt = time.Now().Add(time.Duration(min(token.RefreshExpiresIn, 31536000)) * time.Second)
	}
	return token, nil
}

func (h *Handler) checkTokenTools(ctx context.Context, token tokenMaterial, provider string) ([]mcpclient.Tool, error) {
	if provider == "github" {
		return h.checkTools(ctx, token.Access)
	}
	if token.Grant == nil || token.Grant.Provider != provider {
		return nil, ErrOAuth
	}
	endpoint, err := providerEndpoint(provider)
	if err != nil {
		return nil, err
	}
	session := mcpclient.Session{Client: h.http, Endpoint: endpoint, Token: token.Access, CustomTools: true}
	defer session.Close(ctx)
	if err := session.Initialize(ctx); err != nil {
		return nil, err
	}
	tools, err := session.Tools(ctx)
	if err != nil {
		return nil, err
	}
	filtered := []mcpclient.Tool{}
	for _, tool := range tools {
		if readonlyTool(provider, tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	raw, _ := json.Marshal(filtered)
	raw, err = redactJSON(raw, token.Access, token.Refresh, token.Grant.Client.Secret)
	if err != nil {
		return nil, ErrOAuth
	}

	if json.Unmarshal(raw, &filtered) != nil {
		return nil, ErrOAuth
	}
	return filtered, nil
}

func workspaceID(t tokenMaterial) string {
	if t.Grant == nil || t.Grant.Provider != "notion" {
		return ""
	}
	if _, err := uuid.Parse(t.WorkspaceID); err != nil {
		return ""
	}
	return t.WorkspaceID
}
func workspaceName(t tokenMaterial) string {
	if workspaceID(t) == "" || len(t.WorkspaceName) > 256 {
		return ""
	}
	name := t.WorkspaceName
	for _, secret := range []string{t.Access, t.Refresh, grantSecret(t)} {
		if secret != "" {
			name = strings.ReplaceAll(name, secret, "[REDACTED]")
		}
	}
	return name
}

// Notion uses workers-oauth-provider's user:grant:secret token format.
// Keep this compatibility exception provider-specific; never accept whitespace
// or HTTP header controls, or broaden user-supplied custom Bearer tokens.
func validOAuthAccessToken(provider, token string) bool {
	if bearerTokenPattern.MatchString(token) {
		return true
	}
	if provider != "notion" {
		return false
	}
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if !bearerTokenPattern.MatchString(part) {
			return false
		}
	}
	return true
}
