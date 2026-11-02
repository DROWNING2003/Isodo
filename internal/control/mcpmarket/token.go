package mcpmarket

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

// Row locks serialize refresh token rotation across API replicas. No global
// process mutex or token cache can allow a disconnected grant back into use.
func (h *Handler) freshToken(ctx context.Context, tenant string, ids ...string) (tokenMaterial, error) {
	market := providerID(ids)
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return tokenMaterial{}, ErrOAuth
	}
	defer tx.Rollback(ctx)
	var nonce, sealed []byte
	var status string
	var version int64
	err = tx.QueryRow(ctx, `SELECT status,nonce,ciphertext,version FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2 FOR UPDATE`, tenant, market).Scan(&status, &nonce, &sealed, &version)
	if err != nil || status != "connected" {
		return tokenMaterial{}, ErrOAuth
	}
	raw, err := h.sealer.Open(nonce, sealed)
	var token tokenMaterial
	if err != nil || json.Unmarshal(raw, &token) != nil || token.Access == "" {
		return tokenMaterial{}, ErrOAuth
	}
	if market != "github" && (token.Grant == nil || token.Grant.Provider != market || token.Grant.Tenant != tenant) {
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
		_, err = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET status='expired',nonce=NULL,ciphertext=NULL,version=version+1 WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, market)
		if err == nil {
			_ = tx.Commit(ctx)
		}
		return tokenMaterial{}, ErrOAuth
	}
	next, err := h.exchangeGrant(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.Refresh}}, token.Grant)
	if err != nil {
		if errors.Is(err, errInvalidClient) && token.Grant != nil {
			g := token.Grant
			_, _ = tx.Exec(ctx, `UPDATE mcp_oauth_registrations SET status='invalid' WHERE provider=$1 AND issuer=$2 AND redirect_uri=$3 AND client_id_hash=$4`, g.Provider, g.Metadata.Issuer, g.Redirect, hash(g.Client.ID))
		}
		// A token may already have been rotated if the response was interrupted.
		// Require reconnect instead of automatically replaying refresh.
		_, _ = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET status='expired',nonce=NULL,ciphertext=NULL,version=version+1 WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, market)
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
	_, err = tx.Exec(ctx, `UPDATE mcp_oauth_connections SET nonce=$3,ciphertext=$4,expires_at=$5,updated_at=now() WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, market, nonce, sealed, nullableTime(next.ExpiresAt))
	if err != nil || tx.Commit(ctx) != nil {
		return tokenMaterial{}, ErrOAuth
	}
	next.Version = version
	return next, nil
}
