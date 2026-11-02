package mcpmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DROWNING2003/Isodo/internal/mcpclient"
	"github.com/google/uuid"
)

var bearerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

func customID(market string) (string, bool) {
	id, ok := strings.CutPrefix(market, "custom-")
	parsed, err := uuid.Parse(id)
	return id, ok && err == nil && parsed.String() == id
}

func validCustomEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return publicMCPAddress(ip)
	}
	return !strings.EqualFold(u.Hostname(), "localhost") && !strings.HasSuffix(strings.ToLower(u.Hostname()), ".localhost")
}
func publicMCPAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
func newCustomHTTPClient(endpoint string) *http.Client {
	u, _ := url.Parse(endpoint)
	return &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !validCustomEndpoint(endpoint) || !strings.EqualFold(host, u.Hostname()) || port != "443" {
				return nil, errors.New("MCP endpoint refused")
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("MCP DNS unavailable")
			}
			for _, ip := range ips {
				if !publicMCPAddress(ip) {
					return nil, errors.New("MCP address refused")
				}
			}
			dialer := net.Dialer{Timeout: 10 * time.Second}
			var last error
			for _, ip := range ips {
				c, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return c, nil
				}
				last = e
			}
			return nil, last
		},
	}}
}
func (h *Handler) customClient(endpoint string) *http.Client {
	if h.customHTTP != nil {
		return h.customHTTP
	}
	return newCustomHTTPClient(endpoint)
}
func customEntry(id, name, description, endpoint, kind, status string) map[string]any {
	return map[string]any{"market_mcp_id": "custom-" + id, "custom_server_id": id, "name": name, "provider": "自定义", "description": description, "endpoint": endpoint, "transport": "streamable-http", "auth_kind": kind, "favorite": true, "status": status, "oauth_configured": false, "source": "custom"}
}
func (h *Handler) appendCustom(ctx context.Context, tenant string, items *[]map[string]any) error {
	rows, err := h.pool.Query(ctx, `SELECT id::text,name,description,endpoint,auth_kind,status FROM mcp_custom_servers WHERE tenant_id=$1 ORDER BY created_at,id`, tenant)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, description, endpoint, kind, status string
		if err = rows.Scan(&id, &name, &description, &endpoint, &kind, &status); err != nil {
			return err
		}
		*items = append(*items, customEntry(id, name, description, endpoint, kind, status))
	}
	return rows.Err()
}
func (h *Handler) createCustom(w http.ResponseWriter, r *http.Request, tenant string) {
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Endpoint    string `json:"endpoint"`
		AuthKind    string `json:"auth_kind"`
		Token       string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, 400, "MCP_INVALID_CUSTOM_SERVER")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 80 || utf8.RuneCountInString(input.Description) > 240 || !validCustomEndpoint(input.Endpoint) || len(input.Token) > 4096 || strings.ContainsAny(input.Token, "\r\n") || input.AuthKind != "none" && input.AuthKind != "bearer" || input.AuthKind == "none" && input.Token != "" || input.AuthKind == "bearer" && !bearerTokenPattern.MatchString(input.Token) {
		fail(w, 400, "MCP_INVALID_CUSTOM_SERVER")
		return
	}
	client := h.customClient(input.Endpoint)
	defer client.CloseIdleConnections()
	session := mcpclient.Session{Client: client, Endpoint: input.Endpoint, Token: input.Token, CustomTools: true}
	defer session.Close(r.Context())
	if session.Initialize(r.Context()) != nil {
		fail(w, 502, "MCP_CONNECTION_CHECK_FAILED")
		return
	}
	tools, err := session.Tools(r.Context())
	if err != nil || len(tools) == 0 {
		fail(w, 502, "MCP_CONNECTION_CHECK_FAILED")
		return
	}
	names := map[string]bool{}
	for _, tool := range tools {
		if tool.Name == "" || len(tool.Name) > 128 || strings.ContainsAny(tool.Name, "\r\n\t ") || names[tool.Name] || len(tool.InputSchema) == 0 || tool.InputSchema[0] != '{' {
			fail(w, 502, "MCP_CONNECTION_CHECK_FAILED")
			return
		}
		names[tool.Name] = true
	}
	raw, err := json.Marshal(tools)
	if err != nil || len(raw) > 1<<20 {
		fail(w, 502, "MCP_CONNECTION_CHECK_FAILED")
		return
	}
	if input.Token != "" {
		raw = bytes.ReplaceAll(raw, []byte(input.Token), []byte("[REDACTED]"))
	}
	nonce, sealed, err := h.sealer.Seal([]byte(input.Token))
	if err != nil {
		fail(w, 500, "MCP_SEAL_FAILED")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(r.Context())
	var count int
	if tx.QueryRow(r.Context(), `SELECT 1 FROM tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&count) != nil || tx.QueryRow(r.Context(), `SELECT count(*) FROM mcp_custom_servers WHERE tenant_id=$1`, tenant).Scan(&count) != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	if count >= 63 {
		fail(w, 429, "MCP_CUSTOM_LIMIT")
		return
	}
	id := uuid.NewString()
	_, err = tx.Exec(r.Context(), `INSERT INTO mcp_custom_servers(id,tenant_id,name,description,endpoint,auth_kind,nonce,ciphertext,tools) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, tenant, input.Name, input.Description, input.Endpoint, input.AuthKind, nonce, sealed, raw)
	if err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	write(w, 201, customEntry(id, input.Name, input.Description, input.Endpoint, input.AuthKind, "connected"))
}
func (h *Handler) deleteCustom(w http.ResponseWriter, r *http.Request, tenant, id string) {
	if _, err := uuid.Parse(id); err != nil {
		fail(w, 404, "MCP_NOT_FOUND")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM mcp_custom_servers WHERE id=$1 AND tenant_id=$2`, id, tenant); err != nil {
		fail(w, 503, "MCP_STORAGE_UNAVAILABLE")
		return
	}
	write(w, 204, nil)
}
