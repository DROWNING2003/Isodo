package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type templateScopeAuthenticator struct{ scopes []string }

func (a templateScopeAuthenticator) Authenticate(context.Context, string) (Identity, error) {
	return Identity{TenantID: "account", Credential: "api_key", Scopes: a.scopes}, nil
}
func TestTemplateAPIKeyScopes(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		scopes             []string
		want               int
	}{
		{"sandbox read cannot read templates", "GET", "/v1/templates", []string{"sandboxes:read"}, 403},
		{"template read lists system", "GET", "/v1/system-templates", []string{"templates:read"}, 204},
		{"template read cannot publish", "PUT", "/v1/templates/id/tags/default", []string{"templates:read"}, 403},
		{"template write can publish", "PUT", "/v1/templates/id/tags/default", []string{"templates:write"}, 204},
		{"template read cannot read sandboxes", "GET", "/v1/sandboxes", []string{"templates:read"}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Authorization", "Bearer test")
			response := httptest.NewRecorder()
			Middleware(templateScopeAuthenticator{tc.scopes}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("got %d, want %d", response.Code, tc.want)
			}
		})
	}
}
