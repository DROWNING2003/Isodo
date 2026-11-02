package mcpmarket

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DROWNING2003/Isodo/internal/mcpclient"
	"github.com/google/uuid"
)

// Broker is not a public preview or a general URL proxy. The capability is
// transmitted only Control->Node by mTLS, bound to one DB binding and fenced
// against the current Control lifecycle on every message and while in flight.
func (h *Handler) authorized(ctx context.Context, r *http.Request, expectedVersion ...int64) (string, error) {
	if h.pool == nil {
		return "", ErrOAuth
	}
	id := r.PathValue("bindingID")
	if _, err := uuid.Parse(id); err != nil {
		return "", ErrOAuth
	}
	cap := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(cap) != 43 {
		return "", ErrOAuth
	}
	var tenant string
	version := int64(0)
	if len(expectedVersion) > 0 {
		version = expectedVersion[0]
	}
	var want []byte
	err := h.pool.QueryRow(ctx, `SELECT b.tenant_id::text,b.capability_hash FROM mcp_sandbox_bindings b JOIN sandboxes s ON s.id=b.sandbox_id AND s.tenant_id=b.tenant_id JOIN mcp_oauth_connections c ON c.oauth_connection_id=b.oauth_connection_id AND c.tenant_id=b.tenant_id WHERE b.sandbox_binding_id=$1 AND s.id=$2 AND s.node_id=$3 AND s.ownership_generation=$4 AND s.revision=$5 AND s.desired_state='running' AND s.observed_state='running' AND s.deletion_requested_at IS NULL AND s.deleted_at IS NULL AND c.status='connected' AND ($6::bigint=0 OR c.version=$6)`, id, r.Header.Get("X-Isodo-Sandbox"), r.Header.Get("X-Isodo-Node"), parseUint(r.Header.Get("X-Isodo-Generation")), parseUint(r.Header.Get("X-Isodo-Revision")), version).Scan(&tenant, &want)
	if err != nil || subtle.ConstantTimeCompare(hash(cap), want) != 1 {
		return "", ErrOAuth
	}
	return tenant, nil
}
func (h *Handler) Broker(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	tenant, err := h.authorized(ctx, r)
	if err != nil {
		fail(w, 403, "MCP_BINDING_REVOKED")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		fail(w, 413, "MCP_MESSAGE_TOO_LARGE")
		return
	}
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.JSONRPC != "2.0" {
		fail(w, 400, "MCP_INVALID_MESSAGE")
		return
	}
	reply := func(value any) { write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": value}) }
	switch msg.Method {
	case "initialize":
		reply(map[string]any{"protocolVersion": mcpclient.Protocol, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "isodo-github-readonly", "version": "1"}})
		return
	case "notifications/initialized", "notifications/cancelled":
		write(w, 202, nil)
		return
	case "ping":
		reply(map[string]any{})
		return
	case "tools/list", "tools/call":
	default:
		fail(w, 403, "MCP_METHOD_FORBIDDEN")
		return
	}
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		fail(w, 400, "MCP_ID_REQUIRED")
		return
	}
	if msg.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(msg.Params, &p) != nil || !mcpclient.Allowed(p.Name) {
			fail(w, 403, "MCP_TOOL_FORBIDDEN")
			return
		}
	}
	token, err := h.freshToken(ctx, tenant)
	if err != nil {
		fail(w, 401, "MCP_RECONNECT_REQUIRED")
		return
	}
	// Poll durable authorization for replica-safe unbind/disconnect/lifecycle
	// cancellation. The Node separately fences the captured Guest boot identity.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := h.authorized(ctx, r, token.Version); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	session := mcpclient.Session{Client: h.http, Endpoint: mcpclient.Endpoint, Token: token.Access}
	defer session.Close(ctx)
	if _, err = h.authorized(ctx, r, token.Version); err != nil {
		fail(w, 403, "MCP_BINDING_REVOKED")
		return
	}
	if err = session.Initialize(ctx); err != nil {
		h.upstreamError(w, ctx, tenant, err)
		return
	}
	var result json.RawMessage
	if msg.Method == "tools/list" {
		tools, e := session.Tools(ctx)
		err = e
		result, _ = json.Marshal(map[string]any{"tools": tools})
	} else {
		result, err = session.Request(ctx, "tools/call", msg.Params)
	}
	if err != nil {
		h.upstreamError(w, ctx, tenant, err)
		return
	}
	if _, err = h.authorized(ctx, r, token.Version); err != nil || ctx.Err() != nil {
		fail(w, 403, "MCP_BINDING_REVOKED")
		return
	}
	// Never echo authentication material even if an upstream includes it in data.
	for _, secret := range []string{token.Access, token.Refresh, h.config.ClientSecret} {
		if secret != "" {
			result = bytes.ReplaceAll(result, []byte(secret), []byte("[REDACTED]"))
		}
	}
	reply(result)
}
func (h *Handler) upstreamError(w http.ResponseWriter, ctx context.Context, tenant string, err error) {
	if e, ok := err.(*mcpclient.StatusError); ok && (e.Status == 401 || e.Status == 403) {
		_, _ = h.pool.Exec(ctx, `UPDATE mcp_oauth_connections SET status='expired',nonce=NULL,ciphertext=NULL,version=version+1 WHERE tenant_id=$1 AND market_mcp_id='github'`, tenant)
		fail(w, 401, "MCP_RECONNECT_REQUIRED")
		return
	}
	fail(w, 502, "MCP_UPSTREAM_FAILED")
}
