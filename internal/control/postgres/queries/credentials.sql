-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at <= now();

-- name: SaveOAuthState :exec
INSERT INTO oauth_states (state_hash, nonce, code_verifier, expires_at)
VALUES (sqlc.arg(state_hash), sqlc.arg(nonce), sqlc.arg(code_verifier), sqlc.arg(expires_at));

-- name: ConsumeOAuthState :one
DELETE FROM oauth_states
WHERE state_hash = sqlc.arg(state_hash) AND expires_at > now()
RETURNING nonce, code_verifier;

-- name: DeleteExpiredWebSessions :exec
DELETE FROM web_sessions WHERE expires_at <= now();

-- name: SaveWebSession :exec
INSERT INTO web_sessions (token_hash, tenant_id, csrf_token, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(tenant_id)::text::uuid, sqlc.arg(csrf_token), sqlc.arg(expires_at));

-- name: GetWebSession :one
SELECT i.issuer, i.subject, i.tenant_id::text, COALESCE(i.display_name, ''), s.csrf_token, s.expires_at
FROM web_sessions s
JOIN user_identities i ON i.tenant_id = s.tenant_id
WHERE s.token_hash = sqlc.arg(token_hash) AND s.expires_at > now();

-- name: DeleteWebSession :exec
DELETE FROM web_sessions WHERE token_hash = sqlc.arg(token_hash);
