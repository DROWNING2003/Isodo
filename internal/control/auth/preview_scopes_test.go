package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPreviewRequiresExplicitScope(t *testing.T) {
	for _, scope := range []string{"sandboxes:read", "sandboxes:write", "sandboxes:execute", "sandboxes:preview"} {
		t.Run(scope, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/sandboxes/id/previews", nil)
			r.Header.Set("Authorization", "Bearer key")
			w := httptest.NewRecorder()
			Middleware(templateScopeAuthenticator{[]string{scope}}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			want := 403
			if scope == "sandboxes:preview" {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("status=%d want %d", w.Code, want)
			}
		})
	}
	if !validScopes([]string{"sandboxes:preview"}) || !validScopes([]string{"sandboxes:read", "sandboxes:write", "sandboxes:execute", "sandboxes:preview", "templates:read", "templates:write"}) || validScopes([]string{"sandboxes:preview", "sandboxes:preview"}) {
		t.Fatal("invalid scope validation")
	}
}
