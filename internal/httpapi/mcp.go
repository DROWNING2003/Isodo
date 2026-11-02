package httpapi

import (
	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/mcpmarket"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
)

type MCPHandlers struct{ Market *mcpmarket.Handler }

func (h *MCPHandlers) account(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Market == nil {
		writeError(w, 503, "MCP_UNAVAILABLE", "MCP market is not configured.")
		return
	}
	h.Market.Account(w, r)
}
func (h *MCPHandlers) ListMCPCatalog(w http.ResponseWriter, r *http.Request) {
	h.account(w, r)
}
func (h *MCPHandlers) GetMCPCatalog(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) ListMCPFavorites(w http.ResponseWriter, r *http.Request) {
	h.account(w, r)
}
func (h *MCPHandlers) FavoriteMCP(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) UnfavoriteMCP(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) GetMCPOAuthConnection(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) DisconnectMCPOAuth(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) StartMCPOAuth(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) ListMCPTools(w http.ResponseWriter, r *http.Request, marketID string) {
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) GetMCPOAuthTransaction(w http.ResponseWriter, r *http.Request, transactionID string) {
	r.SetPathValue("transactionID", transactionID)
	h.account(w, r)
}
func (h *MCPHandlers) CancelMCPOAuthTransaction(w http.ResponseWriter, r *http.Request, transactionID string) {
	r.SetPathValue("transactionID", transactionID)
	h.account(w, r)
}
func (h *MCPHandlers) ListSandboxMCPBindings(w http.ResponseWriter, r *http.Request, sandboxID string) {
	r.SetPathValue("sandboxID", sandboxID)
	h.account(w, r)
}
func (h *MCPHandlers) BindSandboxMCP(w http.ResponseWriter, r *http.Request, sandboxID string, marketID string) {
	r.SetPathValue("sandboxID", sandboxID)
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) UnbindSandboxMCP(w http.ResponseWriter, r *http.Request, sandboxID string, marketID string) {
	r.SetPathValue("sandboxID", sandboxID)
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}
func (h *MCPHandlers) SyncSandboxMCP(w http.ResponseWriter, r *http.Request, sandboxID string, marketID string) {
	r.SetPathValue("sandboxID", sandboxID)
	r.SetPathValue("marketID", marketID)
	h.account(w, r)
}

func (h *MCPHandlers) CompleteMCPOAuth(w http.ResponseWriter, r *http.Request, _ api.CompleteMCPOAuthParams) {
	if h == nil || h.Market == nil {
		writeError(w, 503, "MCP_UNAVAILABLE", "MCP market is not configured.")
		return
	}
	h.Market.Callback(w, r)
}

func (h *MCPHandlers) CreateCustomMCP(w http.ResponseWriter, r *http.Request) { h.account(w, r) }
func (h *MCPHandlers) DeleteCustomMCP(w http.ResponseWriter, r *http.Request, customID openapi_types.UUID) {
	h.account(w, r)
}
