package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSandboxCommandRequiresExplicitExecuteScope(t *testing.T) {
	for _, test := range []struct {
		name   string
		scopes []string
		want   int
	}{
		{"management is not execution", []string{"sandboxes:write"}, http.StatusForbidden},
		{"read is not execution", []string{"sandboxes:read"}, http.StatusForbidden},
		{"execute permits invocation", []string{"sandboxes:execute"}, http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/any/commands/run", nil)
			request.Header.Set("Authorization", "Bearer test")
			response := httptest.NewRecorder()
			Middleware(templateScopeAuthenticator{test.scopes}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d", response.Code, test.want)
			}
		})
	}
}
