// Package httpapi joins generated HTTP routes with their domain handlers.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	adminapi "github.com/DROWNING2003/Isodo/internal/api/admin"
	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/control/template"
)

type adminHandlers struct {
	*auth.Web
	*auth.APIKeyHandler
}

var _ adminapi.ServerInterface = (*adminHandlers)(nil)

type sandboxHandlers = sandbox.Handler
type templateHandlers = template.Handler
type publicHandlers struct {
	*sandboxHandlers
	*templateHandlers
}

var _ publicapi.ServerInterface = (*publicHandlers)(nil)

// NewHandler registers both the control-plane and public API contracts.
func NewHandler(web *auth.Web, keys *auth.APIKeyHandler, sandboxes *sandbox.Handler, access *auth.Access, templates ...*template.Handler) http.Handler {
	templateHandler := template.NewHandler(nil)
	if len(templates) > 0 && templates[0] != nil {
		templateHandler = templates[0]
	}
	mux := http.NewServeMux()
	adminapi.HandlerWithOptions(&adminHandlers{Web: web, APIKeyHandler: keys}, adminapi.StdHTTPServerOptions{
		BaseRouter: mux,
		Middlewares: []adminapi.MiddlewareFunc{func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/api-keys" || strings.HasPrefix(r.URL.Path, "/v1/api-keys/") {
					auth.APIKeyManagementMiddleware(access, next).ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
			})
		}},
	})
	publicapi.HandlerWithOptions(&publicHandlers{sandboxHandlers: sandboxes, templateHandlers: templateHandler}, publicapi.StdHTTPServerOptions{
		BaseRouter: mux,
		Middlewares: []publicapi.MiddlewareFunc{func(next http.Handler) http.Handler {
			return auth.Middleware(access, next)
		}},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			if e, ok := errors.AsType[*publicapi.InvalidParamFormatError](err); ok {
				writeError(w, http.StatusBadRequest, "INVALID_"+strings.ToUpper(e.ParamName), "Invalid query parameter.")
				return
			}
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request parameters.")
		},
	})
	return mux
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
