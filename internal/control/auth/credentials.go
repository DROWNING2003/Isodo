package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	adminapi "github.com/DROWNING2003/Isodo/internal/api/admin"
	"net/http"
	"slices"
	"strings"
)

var (
	ErrForbidden = errors.New("insufficient scope")
	ErrCSRF      = errors.New("invalid csrf protection")
	ErrNotFound  = errors.New("credential not found")
	ErrKeyLimit  = errors.New("API key limit reached")
)

type APIKey = adminapi.APIKey

type KeyStore interface {
	CreateAPIKey(context.Context, string, APIKey, []byte) (APIKey, error)
	ListAPIKeys(context.Context, string) ([]APIKey, error)
	RevokeAPIKey(context.Context, string, string) error
	AuthenticateAPIKey(context.Context, string, []byte) (Identity, error)
}

type Access struct {
	Bearer Authenticator
	Keys   KeyStore
	Web    *Web
}

func (a *Access) Authenticate(ctx context.Context, authorization string) (Identity, error) {
	return a.Bearer.Authenticate(ctx, authorization)
}

func (a *Access) AuthenticateRequest(r *http.Request) (Identity, error) {
	headers, keys := r.Header.Values("Authorization"), r.Header.Values("X-API-Key")
	cookies := r.CookiesNamed(sessionCookie)
	if len(headers) > 1 || len(keys) > 1 || len(cookies) > 1 || (len(headers) > 0 && len(keys) > 0) || ((len(headers) > 0 || len(keys) > 0) && len(cookies) > 0) {
		return Identity{}, ErrUnauthenticated
	}
	if len(keys) == 1 {
		id, hash, ok := parseAPIKey(keys[0])
		if !ok {
			return Identity{}, ErrUnauthenticated
		}
		identity, err := a.Keys.AuthenticateAPIKey(r.Context(), id, hash)
		if err != nil {
			return Identity{}, err
		}
		identity.Credential = "api_key"
		return identity, nil
	}
	if len(headers) == 1 {
		return a.Bearer.Authenticate(r.Context(), headers[0])
	}
	if a.Web != nil {
		return a.Web.authenticate(r)
	}
	return Identity{}, ErrUnauthenticated
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(raw string) []byte { sum := sha256.Sum256([]byte(raw)); return sum[:] }

func newAPIKey() (id, secret, raw string) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id = hex.EncodeToString(b)
	secret = randomToken()
	return id, secret, "iso_" + id + "_" + secret
}

func parseAPIKey(raw string) (string, []byte, bool) {
	rest, ok := strings.CutPrefix(raw, "iso_")
	if !ok {
		return "", nil, false
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || len(id) != 32 || len(secret) != 43 {
		return "", nil, false
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(b) != 32 {
		return "", nil, false
	}
	return id, tokenHash(secret), true
}

func validScopes(scopes []string) bool {
	if len(scopes) < 1 || len(scopes) > 4 {
		return false
	}
	for i, scope := range scopes {
		if !slices.Contains([]string{"sandboxes:read", "sandboxes:write", "templates:read", "templates:write"}, scope) {
			return false
		}
		if slices.Contains(scopes[:i], scope) {
			return false
		}
	}
	return true
}
