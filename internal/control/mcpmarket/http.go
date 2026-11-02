// Package mcpmarket owns account favorites, OAuth grants and explicit sandbox bindings.
package mcpmarket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/injection"
	"github.com/DROWNING2003/Isodo/internal/mcpclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool       *pgxpool.Pool
	sealer     *injection.Sealer
	config     Config
	http       *http.Client
	runtime    Runtime
	customHTTP *http.Client // tests may supply a fixture; production uses pinned public HTTPS.
}

func New(pool *pgxpool.Pool, sealer *injection.Sealer, config Config, runtime Runtime) *Handler {
	return &Handler{pool: pool, sealer: sealer, config: config, http: newHTTPClient(), runtime: runtime}
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if status != 204 {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func fail(w http.ResponseWriter, status int, code string) {
	message := "MCP 操作失败，请检查连接状态后重试。"
	switch code {
	case "MCP_DCR_INCOMPATIBLE":
		message = "服务发现或 DCR 注册失败；Isodo 要求 S256、issuer 回调与兼容的动态注册，不会回退 PAT。"
	case "MCP_INVALID_CUSTOM_SERVER":
		message = "请检查名称、HTTPS 地址和认证方式；地址不能包含凭证或查询参数。"
	case "MCP_CONNECTION_CHECK_FAILED":
		message = "连接或工具发现失败，请检查服务是否支持 Streamable HTTP 及认证信息。"
	case "MCP_CUSTOM_LIMIT":
		message = "自定义 MCP 数量已达上限，请先移除不用的连接。"
	}
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (string, bool) {
	i, ok := auth.IdentityFromContext(r.Context())
	if !ok || i.TenantID == "" {
		fail(w, 401, "UNAUTHENTICATED")
		return "", false
	}
	if i.Credential == "api_key" {
		fail(w, 403, "SESSION_REQUIRED")
		return "", false
	}
	if h.pool == nil || h.sealer == nil {
		fail(w, 503, "MCP_UNAVAILABLE")
		return "", false
	}
	return i.TenantID, true
}

// Wrap keeps the OAuth callback separate from account login. All other account
// endpoints pass through the existing session, tenant and CSRF middleware.
func (h *Handler) Wrap(next http.Handler, access auth.Authenticator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/mcp/oauth/callback", h.Callback)
	protected := auth.Middleware(access, http.HandlerFunc(h.Account))
	mux.Handle("/v1/mcp/", protected)
	mux.Handle("/v1/sandboxes/{sandboxID}/mcp", protected)
	mux.Handle("/v1/sandboxes/{sandboxID}/mcp/{marketID}", protected)
	mux.Handle("/v1/sandboxes/{sandboxID}/mcp/{marketID}/sync", protected)
	mux.HandleFunc("POST /internal/mcp/{bindingID}", h.Broker)
	mux.Handle("/", next)
	return mux
}
func (h *Handler) Account(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	tenant, ok := h.identity(w, r)
	if !ok {
		return
	}
	if r.PathValue("sandboxID") != "" {
		h.Bindings(w, r, tenant)
		return
	}
	path := r.URL.Path
	market := r.PathValue("marketID")
	if market == "" {
		parts := strings.Split(path, "/")
		if len(parts) > 4 && (parts[3] == "connections" || parts[3] == "favorites") {
			market = parts[4]
		}
	}
	switch {
	case path == "/v1/mcp/custom" && r.Method == "POST":
		h.createCustom(w, r, tenant)
	case strings.HasPrefix(path, "/v1/mcp/custom/") && r.Method == "DELETE":
		h.deleteCustom(w, r, tenant, strings.TrimPrefix(path, "/v1/mcp/custom/"))
	case path == "/v1/mcp/catalog" && r.Method == "GET", strings.HasPrefix(path, "/v1/mcp/catalog/") && r.Method == "GET", path == "/v1/mcp/favorites" && r.Method == "GET":
		h.catalog(w, r, tenant)
	case officialProvider(market) && path == "/v1/mcp/favorites/"+market && (r.Method == "PUT" || r.Method == "DELETE"):
		sql := `INSERT INTO mcp_favorites(tenant_id,market_mcp_id) VALUES($1,$2) ON CONFLICT DO NOTHING`
		if r.Method == "DELETE" {
			sql = `DELETE FROM mcp_favorites WHERE tenant_id=$1 AND market_mcp_id=$2`
		}
		if _, err := h.pool.Exec(r.Context(), sql, tenant, market); err != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		write(w, 204, nil)
	case officialProvider(market) && path == "/v1/mcp/connections/"+market && r.Method == "GET":
		h.connection(w, r, tenant, market)
	case officialProvider(market) && path == "/v1/mcp/connections/"+market && r.Method == "DELETE":
		tx, err := h.pool.Begin(r.Context())
		if err != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		defer tx.Rollback(r.Context())
		_, err = tx.Exec(r.Context(), `UPDATE mcp_oauth_connections SET status='disconnected',nonce=NULL,ciphertext=NULL,tools='[]',version=version+1,updated_at=now() WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, market)
		if err == nil {
			_, err = tx.Exec(r.Context(), `UPDATE mcp_oauth_transactions SET status='cancelled' WHERE tenant_id=$1 AND market_mcp_id=$2 AND status IN ('pending','processing')`, tenant, market)
		}
		if err != nil || tx.Commit(r.Context()) != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		write(w, 204, nil)
	case officialProvider(market) && path == "/v1/mcp/connections/"+market+"/authorize" && r.Method == "POST":
		h.Start(w, r, tenant, market)
	case officialProvider(market) && path == "/v1/mcp/connections/"+market+"/tools" && r.Method == "GET":
		token, err := h.freshToken(r.Context(), tenant, market)
		if err != nil {
			fail(w, 409, "MCP_RECONNECT_REQUIRED")
			return
		}
		tools, err := h.checkTokenTools(r.Context(), token, market)
		if err != nil {
			fail(w, 502, "MCP_UPSTREAM_UNAVAILABLE")
			return
		}
		write(w, 200, map[string]any{"items": tools})
	case len(path) > len("/v1/mcp/oauth/transactions/") && path[:len("/v1/mcp/oauth/transactions/")] == "/v1/mcp/oauth/transactions/":
		id := path[len("/v1/mcp/oauth/transactions/"):]
		if _, err := uuid.Parse(id); err != nil {
			fail(w, 404, "MCP_TRANSACTION_NOT_FOUND")
			return
		}
		if r.Method == "DELETE" {
			_, err := h.pool.Exec(r.Context(), `UPDATE mcp_oauth_transactions SET status='cancelled',ciphertext=''::bytea,nonce=''::bytea WHERE transaction_id=$1 AND tenant_id=$2 AND status IN ('pending','processing')`, id, tenant)
			if err != nil {
				fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
				return
			}
			write(w, 204, nil)
			return
		}
		if r.Method != "GET" {
			fail(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		var status string
		var expires time.Time
		err := h.pool.QueryRow(r.Context(), `SELECT status,expires_at FROM mcp_oauth_transactions WHERE transaction_id=$1 AND tenant_id=$2`, id, tenant).Scan(&status, &expires)
		if err != nil {
			fail(w, 404, "MCP_TRANSACTION_NOT_FOUND")
			return
		}
		if (status == "pending" || status == "processing") && !expires.After(time.Now()) {
			status = "expired"
		}
		write(w, 200, map[string]any{"transaction_id": id, "status": status, "expires_at": expires})
	default:
		fail(w, 404, "MCP_NOT_FOUND")
	}
}
func (h *Handler) catalog(w http.ResponseWriter, r *http.Request, tenant string) {
	for _, market := range []string{"github", "notion"} {
		var expired bool
		if h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2 AND status='connected' AND expires_at<=now()+interval '30 seconds')`, tenant, market).Scan(&expired) == nil && expired {
			_, _ = h.freshToken(r.Context(), tenant, market)
		}
	}
	rows, err := h.pool.Query(r.Context(), `SELECT c.market_mcp_id,c.name,c.provider,c.description,c.endpoint,c.transport,c.auth_kind,f.market_mcp_id IS NOT NULL,COALESCE(o.status,'disconnected'),o.oauth_connection_id::text,o.expires_at,o.project_ref,o.workspace_id,o.workspace_name FROM mcp_catalog c LEFT JOIN mcp_favorites f ON f.market_mcp_id=c.market_mcp_id AND f.tenant_id=$1 LEFT JOIN mcp_oauth_connections o ON o.market_mcp_id=c.market_mcp_id AND o.tenant_id=$1 WHERE c.market_mcp_id IN ('github','notion') ORDER BY c.market_mcp_id`, tenant)
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, provider, description, endpoint, transport, kind, status string
		var favorite bool
		var connection *string
		var expiry *time.Time
		var project, workspaceID, workspaceName *string
		if rows.Scan(&id, &name, &provider, &description, &endpoint, &transport, &kind, &favorite, &status, &connection, &expiry, &project, &workspaceID, &workspaceName) != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		if r.URL.Path == "/v1/mcp/favorites" && !favorite {
			continue
		}
		if status == "connected" && expiry != nil && !expiry.After(time.Now()) {
			status = "expired"
		}
		items = append(items, map[string]any{"market_mcp_id": id, "name": name, "provider": provider, "description": description, "endpoint": endpoint, "transport": transport, "auth_kind": kind, "favorite": favorite, "status": status, "oauth_connection_id": connection, "oauth_configured": (id == "github" && configured(h.config) || id != "github" && callbackConfigured(h.config)), "project_ref": project, "workspace_id": workspaceID, "workspace_name": workspaceName, "source": "market"})
	}
	if rows.Err() != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	if err := h.appendCustom(r.Context(), tenant, &items); err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	if wanted, detail := strings.CutPrefix(r.URL.Path, "/v1/mcp/catalog/"); detail {
		filtered := []map[string]any{}
		for _, item := range items {
			if item["market_mcp_id"] == wanted {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			fail(w, 404, "MCP_NOT_FOUND")
			return
		}
		items = filtered
	}
	write(w, 200, map[string]any{"items": items})
}
func (h *Handler) connection(w http.ResponseWriter, r *http.Request, tenant string, ids ...string) {
	market := providerID(ids)
	var id, status string
	var tools json.RawMessage
	var expiry *time.Time
	var project *string
	err := h.pool.QueryRow(r.Context(), `SELECT oauth_connection_id::text,status,tools,expires_at,project_ref FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, market).Scan(&id, &status, &tools, &expiry, &project)
	if errors.Is(err, pgx.ErrNoRows) {
		write(w, 200, map[string]any{"status": "disconnected"})
		return
	}
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	if status == "connected" && expiry != nil && !expiry.After(time.Now()) {
		status = "expired"
	}
	write(w, 200, map[string]any{"project_ref": project, "market_mcp_id": market, "oauth_connection_id": id, "status": status, "tools": tools, "expires_at": expiry})
}
func (h *Handler) Start(w http.ResponseWriter, r *http.Request, tenant string, ids ...string) {
	market := providerID(ids)
	if !officialProvider(market) || market == "github" && !configured(h.config) || market != "github" && !callbackConfigured(h.config) {
		fail(w, 503, "MCP_OAUTH_NOT_CONFIGURED")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var m metadata
	var err error
	var g *grantContext
	clientID := h.config.ClientID
	resource := mcpclient.Endpoint
	if market == "github" {
		m, err = h.discover(ctx)
	} else {
		var discovered grantContext
		discovered, err = h.discoverProvider(ctx, market)
		if err == nil {
			discovered.Client, err = h.registerClient(ctx, discovered)
		}
		if err == nil {
			discovered.Tenant = tenant
			g = &discovered
			m = discovered.Metadata
			clientID = g.Client.ID
			resource = g.Resource
		}
	}
	if err != nil {
		if market == "github" {
			fail(w, 502, "MCP_DISCOVERY_FAILED")
		} else {
			fail(w, 502, "MCP_DCR_INCOMPATIBLE")
		}
		return
	}
	state, verifier, id := randomValue(), randomValue(), uuid.NewString()
	challenge := base64.RawURLEncoding.EncodeToString(hash(verifier))
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, "mcp-start:"+tenant); err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	// Serialize account starts, disconnect and callback version changes via the grant row.
	_, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_connections(oauth_connection_id,tenant_id,market_mcp_id,status) VALUES($1,$2,$3,'disconnected') ON CONFLICT DO NOTHING`, uuid.NewString(), tenant, market)
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	var version int64
	err = tx.QueryRow(ctx, `SELECT version FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2 FOR UPDATE`, tenant, market).Scan(&version)
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	raw, _ := json.Marshal(oauthPending{Verifier: verifier, RedirectURI: h.config.RedirectURI, Metadata: m, ConnectionVersion: version, Grant: g})
	nonce, sealed, err := h.sealer.Seal(raw)
	if err != nil {
		fail(w, 500, "MCP_SEAL_FAILED")
		return
	}
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_oauth_transactions WHERE tenant_id=$1 AND status IN ('pending','processing') AND expires_at>now()`, tenant).Scan(&count)
	if err != nil || count >= 4 {
		fail(w, 429, "MCP_AUTHORIZATION_LIMIT")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_transactions(transaction_id,tenant_id,market_mcp_id,state_hash,nonce,ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,$6,now()+interval '5 minutes')`, id, tenant, market, hash(state), nonce, sealed)
	if err != nil || tx.Commit(ctx) != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	values := url.Values{"client_id": {clientID}, "redirect_uri": {h.config.RedirectURI}, "response_type": {"code"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {resource}}
	if market == "notion" {
		values.Set("prompt", "consent")
	}
	if g != nil && g.Scope != "" {
		values.Set("scope", g.Scope)
	}
	// GitHub Apps derive minimal read permissions from installation settings. No
	// broad repo scope is requested (OAuth Apps therefore only see public data).
	write(w, 200, map[string]any{"transaction_id": id, "authorization_url": m.Authorization + "?" + values.Encode(), "expires_at": time.Now().Add(5 * time.Minute)})
}
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if h.pool == nil || h.sealer == nil || !callbackConfigured(h.config) {
		fail(w, 503, "MCP_OAUTH_NOT_CONFIGURED")
		return
	}
	expectedCallback, _ := url.Parse(h.config.RedirectURI)
	if r.Host != expectedCallback.Host || r.URL.Path != expectedCallback.Path {
		fail(w, 400, "MCP_INVALID_CALLBACK")
		return
	}
	state := r.URL.Query().Get("state")
	if len(state) != 43 || len(r.URL.Query()["state"]) != 1 {
		fail(w, 400, "MCP_INVALID_STATE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var id, tenant, market string
	var nonce, sealed []byte
	err := h.pool.QueryRow(ctx, `UPDATE mcp_oauth_transactions SET status='processing' WHERE state_hash=$1 AND status='pending' AND expires_at>now() RETURNING transaction_id::text,tenant_id::text,nonce,ciphertext,market_mcp_id`, hash(state)).Scan(&id, &tenant, &nonce, &sealed, &market)
	if err != nil {
		fail(w, 400, "MCP_INVALID_STATE")
		return
	}
	success := false
	defer func() {
		status := "failed"
		if success {
			status = "connected"
		}
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_, _ = h.pool.Exec(cleanup, `UPDATE mcp_oauth_transactions SET status=$2,nonce=''::bytea,ciphertext=''::bytea WHERE transaction_id=$1 AND status='processing'`, id, status)
	}()
	raw, err := h.sealer.Open(nonce, sealed)
	var p oauthPending
	if err != nil || json.Unmarshal(raw, &p) != nil || p.RedirectURI != h.config.RedirectURI || !officialProvider(market) {
		fail(w, 400, "MCP_AUTHORIZATION_FAILED")
		return
	}
	if market == "github" && p.Grant != nil {
		fail(w, 400, "MCP_AUTHORIZATION_FAILED")
		return
	}
	expectedIssuer := issuer
	if market != "github" {
		if p.Grant == nil || p.Grant.Provider != market || p.Grant.Tenant != tenant || !oauthURL(market, p.Grant.Metadata.Issuer) {
			fail(w, 400, "MCP_AUTHORIZATION_FAILED")
			return
		}
		expectedIssuer = p.Grant.Metadata.Issuer
	}
	responseIssuer := r.URL.Query().Get("iss")
	if p.Metadata.Issuer != expectedIssuer || r.URL.Query().Get("error") != "" || len(r.URL.Query()["iss"]) > 1 || responseIssuer != "" && responseIssuer != expectedIssuer || market != "github" && responseIssuer == "" {
		slog.Warn("MCP OAuth callback rejected", "provider", market, "stage", "issuer", "issuer_present", responseIssuer != "", "issuer_matches", responseIssuer == expectedIssuer, "provider_error", r.URL.Query().Get("error") != "")
		fail(w, 400, "MCP_AUTHORIZATION_FAILED")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 2048 || len(r.URL.Query()["code"]) != 1 {
		fail(w, 400, "MCP_AUTHORIZATION_FAILED")
		return
	}
	token, err := h.exchangeGrant(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.RedirectURI}, "code_verifier": {p.Verifier}}, p.Grant)
	if err != nil {
		slog.Warn("MCP OAuth callback rejected", "provider", market, "stage", "token_exchange", "invalid_client", errors.Is(err, errInvalidClient))
		if errors.Is(err, errInvalidClient) && p.Grant != nil {
			g := p.Grant
			_, _ = h.pool.Exec(ctx, `UPDATE mcp_oauth_registrations SET status='invalid' WHERE provider=$1 AND issuer=$2 AND redirect_uri=$3 AND client_id_hash=$4`, g.Provider, g.Metadata.Issuer, g.Redirect, hash(g.Client.ID))
		}
		fail(w, 400, "MCP_AUTHORIZATION_FAILED")
		return
	}
	tools, err := h.checkTokenTools(ctx, token, market)
	if err != nil || len(tools) == 0 {
		fail(w, 502, "MCP_CONNECTION_CHECK_FAILED")
		return
	}
	data, _ := json.Marshal(token)
	nonce, sealed, err = h.sealer.Seal(data)
	if err != nil {
		fail(w, 500, "MCP_SEAL_FAILED")
		return
	}
	toolData, _ := json.Marshal(tools)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	// Keep lock ordering consistent with disconnect: grant first, transaction second.
	var version int64
	err = tx.QueryRow(ctx, `SELECT version FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2 FOR UPDATE`, tenant, market).Scan(&version)
	if err != nil || version != p.ConnectionVersion {
		fail(w, 409, "MCP_AUTHORIZATION_CANCELLED")
		return
	}
	var transactionID string
	err = tx.QueryRow(ctx, `SELECT transaction_id::text FROM mcp_oauth_transactions WHERE transaction_id=$1 AND status='processing' AND expires_at>now() FOR UPDATE`, id).Scan(&transactionID)
	if err != nil {
		fail(w, 409, "MCP_AUTHORIZATION_CANCELLED")
		return
	}
	_, err = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET status='connected',nonce=$2,ciphertext=$3,tools=$4,expires_at=$5,version=version+1,updated_at=now(),project_ref=$7,workspace_id=NULLIF($8,''),workspace_name=NULLIF($9,'') WHERE tenant_id=$1 AND market_mcp_id=$6`, tenant, nonce, sealed, toolData, nullableTime(token.ExpiresAt), market, nil, workspaceID(token), workspaceName(token))
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE mcp_oauth_transactions SET status='connected',nonce=''::bytea,ciphertext=''::bytea WHERE transaction_id=$1`, id)
	}
	if err != nil || tx.Commit(ctx) != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}

	success = true
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("MCP 授权并完成只读工具发现。请返回 Isodo，关闭此页面。"))
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func (h *Handler) checkTools(ctx context.Context, token string) ([]mcpclient.Tool, error) {
	s := mcpclient.Session{Client: h.http, Endpoint: mcpclient.Endpoint, Token: token}
	defer s.Close(ctx)
	if err := s.Initialize(ctx); err != nil {
		return nil, err
	}
	tools, err := s.Tools(ctx)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(tools)
	raw, err = redactJSON(raw, token, h.config.ClientSecret)
	if err != nil {
		return nil, ErrOAuth
	}

	if json.Unmarshal(raw, &tools) != nil {
		return nil, ErrOAuth
	}
	return tools, nil
}
