package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/jackc/pgx/v5"
)

func (s *Store) SaveOAuthState(ctx context.Context, hash []byte, state auth.OAuthState, expires time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at <= now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO oauth_states(state_hash,nonce,code_verifier,expires_at) VALUES($1,$2,$3,$4)`, hash, state.Nonce, state.Verifier, expires)
	return err
}
func (s *Store) ConsumeOAuthState(ctx context.Context, hash []byte) (auth.OAuthState, error) {
	var state auth.OAuthState
	err := s.pool.QueryRow(ctx, `DELETE FROM oauth_states WHERE state_hash=$1 AND expires_at>now() RETURNING nonce,code_verifier`, hash).Scan(&state.Nonce, &state.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, auth.ErrUnauthenticated
	}
	return state, err
}
func (s *Store) SaveSession(ctx context.Context, hash []byte, session auth.Session) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE expires_at <= now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO web_sessions(token_hash,tenant_id,csrf_token,expires_at) VALUES($1,$2::uuid,$3,$4)`, hash, session.Identity.TenantID, session.CSRF, session.ExpiresAt)
	return err
}
func (s *Store) GetSession(ctx context.Context, hash []byte) (auth.Session, error) {
	var session auth.Session
	err := s.pool.QueryRow(ctx, `SELECT i.issuer,i.subject,i.tenant_id::text,COALESCE(i.display_name,''),s.csrf_token,s.expires_at FROM web_sessions s JOIN user_identities i ON i.tenant_id=s.tenant_id WHERE s.token_hash=$1 AND s.expires_at>now()`, hash).Scan(&session.Identity.Issuer, &session.Identity.Subject, &session.Identity.TenantID, &session.Identity.DisplayName, &session.CSRF, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, auth.ErrUnauthenticated
	}
	return session, err
}
func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE token_hash=$1`, hash)
	return err
}

func (s *Store) CreateAPIKey(ctx context.Context, tenant string, key auth.APIKey, hash []byte) (auth.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return key, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,1))`, tenant); err != nil {
		return key, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE tenant_id=$1::uuid AND revoked_at IS NULL`, tenant).Scan(&count); err != nil {
		return key, err
	}
	if count >= 10 {
		return key, auth.ErrKeyLimit
	}
	result, err := scanAPIKey(tx.QueryRow(ctx, `INSERT INTO api_keys(id,tenant_id,name,secret_hash,prefix,scopes,expires_at) VALUES($1,$2::uuid,$3,$4,$5,$6,$7) RETURNING id,name,prefix,scopes,created_at,expires_at,last_used_at,revoked_at`, key.ID, tenant, key.Name, hash, key.Prefix, key.Scopes, key.ExpiresAt))
	if err != nil {
		return key, err
	}
	if err = tx.Commit(ctx); err != nil {
		return key, err
	}
	return result, nil
}
func (s *Store) ListAPIKeys(ctx context.Context, tenant string) ([]auth.APIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,prefix,scopes,created_at,expires_at,last_used_at,revoked_at FROM api_keys WHERE tenant_id=$1::uuid ORDER BY created_at DESC,id DESC`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]auth.APIKey, 0)
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
func (s *Store) RevokeAPIKey(ctx context.Context, tenant, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND tenant_id=$2::uuid`, id, tenant)
	if err == nil && tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return err
}
func (s *Store) AuthenticateAPIKey(ctx context.Context, id string, hash []byte) (auth.Identity, error) {
	var expected []byte
	err := s.pool.QueryRow(ctx, `SELECT secret_hash FROM api_keys WHERE id=$1`, id).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Identity{}, err
	}
	if subtle.ConstantTimeCompare(hash, expected) != 1 {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	var identity auth.Identity
	// This conditional update is the authentication linearization point: a key
	// revoked or expired after the lookup cannot authorize the request.
	err = s.pool.QueryRow(ctx, `UPDATE api_keys SET last_used_at=now() WHERE id=$1 AND secret_hash=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()) RETURNING tenant_id::text,scopes`, id, hash).Scan(&identity.TenantID, &identity.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity, auth.ErrUnauthenticated
	}
	return identity, err
}
func scanAPIKey(row rowScanner) (auth.APIKey, error) {
	var key auth.APIKey
	err := row.Scan(&key.ID, &key.Name, &key.Prefix, &key.Scopes, &key.CreatedAt, &key.ExpiresAt, &key.LastUsedAt, &key.RevokedAt)
	return key, err
}
