package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

type Authenticator interface {
	Authenticate(context.Context, string) (Identity, error)
}
type requestAuthenticator interface {
	AuthenticateRequest(*http.Request) (Identity, error)
}
type identityContextKey struct{}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	i, ok := ctx.Value(identityContextKey{}).(Identity)
	return i, ok
}

func Middleware(authenticator Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var identity Identity
		var err error
		if a, ok := authenticator.(requestAuthenticator); ok {
			identity, err = a.AuthenticateRequest(r)
		} else {
			headers := r.Header.Values("Authorization")
			if len(headers) != 1 || len(r.Header.Values("X-API-Key")) != 0 {
				err = ErrUnauthenticated
			} else {
				identity, err = authenticator.Authenticate(r.Context(), headers[0])
			}
		}
		if err != nil {
			writeAuthenticationError(w, err)
			return
		}
		if identity.Credential == "api_key" {
			resource := "sandboxes"
			if r.URL.Path == "/v1/system-templates" || r.URL.Path == "/v1/templates" || strings.HasPrefix(r.URL.Path, "/v1/templates/") {
				resource = "templates"
			}
			scope := resource + ":write"
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				scope = resource + ":read"
			}
			if !slices.Contains(identity.Scopes, scope) {
				writeAuthenticationError(w, ErrForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity)))
	})
}

func writeAuthenticationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		w.Header().Set("WWW-Authenticate", `Bearer realm="isodo-api"`)
		writeAuthError(w, 401, "UNAUTHENTICATED", "Please sign in or supply a valid API credential.")
	case errors.Is(err, ErrForbidden):
		writeAuthError(w, 403, "INSUFFICIENT_SCOPE", "This credential does not grant the required permission.")
	case errors.Is(err, ErrCSRF):
		writeAuthError(w, 403, "INVALID_CSRF", "The request could not be verified. Refresh and try again.")
	default:
		writeAuthError(w, 503, "AUTHENTICATION_UNAVAILABLE", "Authentication is temporarily unavailable.")
	}
}
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
