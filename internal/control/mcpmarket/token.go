package mcpmarket

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

// Row locks serialize refresh token rotation across API replicas. No global
// process mutex or token cache can allow a disconnected grant back into use.
func (h *Handler) freshToken(ctx context.Context, tenant string) (tokenMaterial, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return tokenMaterial{}, ErrOAuth
	}
	defer tx.Rollback(ctx)
	var nonce, sealed []byte
	var status string
	var version int64
	err = tx.QueryRow(ctx, `SELECT status,nonce,ciphertext,version FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id='github' FOR UPDATE`, tenant).Scan(&status, &nonce, &sealed, &version)
	if err != nil || status != "connected" {
		return tokenMaterial{}, ErrOAuth
	}
	raw, err := h.sealer.Open(nonce, sealed)
	var token tokenMaterial
	if err != nil || json.Unmarshal(raw, &token) != nil || token.Access == "" {
		return tokenMaterial{}, ErrOAuth
	}
	token.Version = version
	if token.ExpiresAt.IsZero() || time.Now().Add(30*time.Second).Before(token.ExpiresAt) {
		if tx.Commit(ctx) != nil {
			return tokenMaterial{}, ErrOAuth
		}
		return token, nil
	}
	if token.Refresh == "" || !token.RefreshExpiresAt.IsZero() && !token.RefreshExpiresAt.After(time.Now()) {
		_, err = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET status='expired',nonce=NULL,ciphertext=NULL,version=version+1 WHERE tenant_id=$1 AND market_mcp_id='github'`, tenant)
		if err == nil {
			_ = tx.Commit(ctx)
		}
		return tokenMaterial{}, ErrOAuth
	}
	next, err := h.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.Refresh}})
	if err != nil {
		// A token may already have been rotated if the response was interrupted.
		// Require reconnect instead of automatically replaying refresh.
		_, _ = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET status='expired',nonce=NULL,ciphertext=NULL,version=version+1 WHERE tenant_id=$1 AND market_mcp_id='github'`, tenant)
		_ = tx.Commit(ctx)
		return tokenMaterial{}, ErrOAuth
	}
	if next.Refresh == "" {
		next.Refresh = token.Refresh
		next.RefreshExpiresAt = token.RefreshExpiresAt
	}
	raw, _ = json.Marshal(next)
	nonce, sealed, err = h.sealer.Seal(raw)
	if err != nil {
		return tokenMaterial{}, ErrOAuth
	}
	_, err = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET nonce=$2,ciphertext=$3,expires_at=$4,updated_at=now() WHERE tenant_id=$1 AND market_mcp_id='github'`, tenant, nonce, sealed, nullableTime(next.ExpiresAt))
	if err != nil || tx.Commit(ctx) != nil {
		return tokenMaterial{}, ErrOAuth
	}
	next.Version = version
	return next, nil
}
