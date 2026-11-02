package mcpmarket

import (
	"context"
	"net/http"
	"strconv"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/google/uuid"
)

type Runtime interface {
	NodeID() string
	SyncMCP(context.Context, *nodev1.SyncMCPRequest) (*nodev1.SyncMCPResponse, error)
}

func (h *Handler) target(ctx context.Context, tenant, sandbox string) (*nodev1.SandboxTarget, error) {
	var generation, revision uint64
	var nodeID string
	if h.runtime == nil {
		return nil, ErrOAuth
	}
	err := h.pool.QueryRow(ctx, `SELECT ownership_generation,revision,COALESCE(node_id,'') FROM sandboxes WHERE id=$1 AND tenant_id=$2 AND desired_state!='deleted' AND deleted_at IS NULL`, sandbox, tenant).Scan(&generation, &revision, &nodeID)
	if err != nil || generation == 0 || revision == 0 || nodeID != h.runtime.NodeID() {
		return nil, ErrOAuth
	}
	return &nodev1.SandboxTarget{SandboxId: sandbox, OwnershipGeneration: generation, Revision: revision}, nil
}
func (h *Handler) Bindings(w http.ResponseWriter, r *http.Request, tenant string) {
	sandbox := r.PathValue("sandboxID")
	if _, err := uuid.Parse(sandbox); err != nil {
		fail(w, 404, "SANDBOX_NOT_FOUND")
		return
	}
	var owner bool
	err := h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sandboxes WHERE id=$1 AND tenant_id=$2 AND desired_state!='deleted' AND deleted_at IS NULL)`, sandbox, tenant).Scan(&owner)
	if err != nil || !owner {
		fail(w, 404, "SANDBOX_NOT_FOUND")
		return
	}
	market := r.PathValue("marketID")
	if r.Method == "GET" && market == "" {
		rows, err := h.pool.Query(r.Context(), `SELECT b.sandbox_binding_id::text,b.market_mcp_id,b.oauth_connection_id::text,b.sync_status,c.status FROM mcp_sandbox_bindings b JOIN mcp_oauth_connections c USING(tenant_id,oauth_connection_id) WHERE b.tenant_id=$1 AND b.sandbox_id=$2`, tenant, sandbox)
		if err != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		defer rows.Close()
		items := []map[string]string{}
		for rows.Next() {
			var id, m, c, s, status string
			if rows.Scan(&id, &m, &c, &s, &status) != nil {
				fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
				return
			}
			items = append(items, map[string]string{"sandbox_binding_id": id, "market_mcp_id": m, "oauth_connection_id": c, "sync_status": s, "status": status})
		}
		if rows.Err() != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if market != "github" {
		fail(w, 404, "MCP_NOT_FOUND")
		return
	}
	switch r.Method {
	case "PUT":
		// A favorite alone is insufficient. Only a live, checked grant can bind.
		if _, err := h.freshToken(r.Context(), tenant); err != nil {
			fail(w, 409, "MCP_RECONNECT_REQUIRED")
			return
		}
		id, cap := uuid.NewString(), randomValue()
		_, err = h.pool.Exec(r.Context(), `INSERT INTO mcp_sandbox_bindings(sandbox_binding_id,tenant_id,sandbox_id,market_mcp_id,oauth_connection_id,capability_hash) SELECT $1,$2,$3,'github',oauth_connection_id,$4 FROM mcp_oauth_connections WHERE tenant_id=$2 AND market_mcp_id='github' AND status='connected' ON CONFLICT(sandbox_id,market_mcp_id) DO NOTHING`, id, tenant, sandbox, hash(cap))
		if err != nil {
			fail(w, 409, "MCP_BINDING_FAILED")
			return
		}
		h.sync(w, r, tenant, sandbox)
	case "DELETE":
		// Commit revocation before attempting Node config sync. Even an unreachable
		// Node cannot continue calling through a deleted capability.
		_, err = h.pool.Exec(r.Context(), `DELETE FROM mcp_sandbox_bindings WHERE tenant_id=$1 AND sandbox_id=$2 AND market_mcp_id=$3`, tenant, sandbox, market)
		if err != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		h.sync(w, r, tenant, sandbox)
	case "POST":
		if r.URL.Path[len(r.URL.Path)-5:] != "/sync" {
			fail(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		h.sync(w, r, tenant, sandbox)
	default:
		fail(w, 405, "METHOD_NOT_ALLOWED")
	}
}
func (h *Handler) sync(w http.ResponseWriter, r *http.Request, tenant, sandbox string) {
	target, err := h.target(r.Context(), tenant, sandbox)
	if err != nil {
		write(w, 202, map[string]any{"sync_status": "pending", "restart_required": true, "message": "绑定已保存，Node 尚不可用；请启动沙箱后重试同步。"})
		return
	}
	// Rotate and persist only hashes, then send new capabilities over existing
	// mTLS. A partial failure is fail-closed and explicitly retryable.
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(r.Context())
	rows, err := tx.Query(r.Context(), `SELECT sandbox_binding_id::text,market_mcp_id FROM mcp_sandbox_bindings WHERE tenant_id=$1 AND sandbox_id=$2 ORDER BY sandbox_binding_id FOR UPDATE`, tenant, sandbox)
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	bindings := []*nodev1.MCPBinding{}
	for rows.Next() {
		var id, market string
		if rows.Scan(&id, &market) != nil {
			rows.Close()
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
		bindings = append(bindings, &nodev1.MCPBinding{BindingId: id, MarketMcpId: market, Capability: randomValue()})
	}
	rows.Close()
	if rows.Err() != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	for _, b := range bindings {
		_, err = tx.Exec(r.Context(), `UPDATE mcp_sandbox_bindings SET capability_hash=$2,sync_status='pending' WHERE sandbox_binding_id=$1`, b.BindingId, hash(b.Capability))
		if err != nil {
			fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
			return
		}
	}
	if tx.Commit(r.Context()) != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	result, err := h.runtime.SyncMCP(r.Context(), &nodev1.SyncMCPRequest{Target: target, Bindings: bindings})
	if err != nil {
		for _, b := range bindings {
			_, _ = h.pool.Exec(r.Context(), `UPDATE mcp_sandbox_bindings SET sync_status='failed' WHERE sandbox_binding_id=$1 AND capability_hash=$2`, b.BindingId, hash(b.Capability))
		}
		write(w, 202, map[string]any{"sync_status": "failed", "restart_required": true, "message": "绑定已保存或撤销，但 Guest 配置同步失败。启动沙箱后点击应用重试。"})
		return
	}
	for _, b := range bindings {
		_, err = h.pool.Exec(r.Context(), `UPDATE mcp_sandbox_bindings SET sync_status='applied' WHERE sandbox_binding_id=$1 AND capability_hash=$2`, b.BindingId, hash(b.Capability))
		if err != nil {
			break
		}
	}
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	write(w, 200, map[string]any{"sync_status": "applied", "restart_required": result.RestartRequired})
}
func parseUint(s string) uint64 { n, _ := strconv.ParseUint(s, 10, 64); return n }
