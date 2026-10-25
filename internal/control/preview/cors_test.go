package preview

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPreviewCookieBootstrapAndUpstreamCORS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	p, err := New(Config{Secret: "test-secret", Domain: "isodo.localhost", AppOrigin: "http://app.isodo.localhost:5734"})
	if err != nil {
		t.Fatal(err)
	}
	const id = "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"
	base, auth, err := p.Publish(id, 8080, upstream.URL+"/pair#token=one-time")
	if err != nil {
		t.Fatal(err)
	}
	handler := p.Wrap(http.NotFoundHandler())
	r := httptest.NewRequest(http.MethodGet, auth, nil)
	r.Header.Set("Origin", p.appOrigin)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	if w.Code != http.StatusFound || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("preview did not establish the protected cookie")
	}
	for _, path := range []string{"__isodo/ready", ".well-known/t3/environment"} {
		r = httptest.NewRequest(http.MethodGet, base+path, nil)
		r.Header.Set("Origin", p.appOrigin)
		r.AddCookie(cookies[0])
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
			t.Fatalf("bootstrap/proxy status=%d", w.Code)
		}
		if got := w.Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != p.appOrigin {
			t.Fatalf("CORS headers=%v", got)
		}
	}
	r = httptest.NewRequest(http.MethodGet, base+"__isodo/ready", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: p.mint(id, 8080, time.Now().Add(-time.Minute))})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired preview accepted: %d", w.Code)
	}
}

func TestPreviewPreflightAllowsOnlyConfiguredAppOrigin(t *testing.T) {
	p, err := New(Config{Secret: "test-secret", Domain: "isodo.localhost", AppOrigin: "http://app.isodo.localhost:5734"})
	if err != nil {
		t.Fatal(err)
	}
	handler := p.Wrap(http.NotFoundHandler())
	for _, origin := range []string{"http://app.isodo.localhost:5734", "http://evil.isodo.localhost:5734"} {
		r := httptest.NewRequest(http.MethodOptions, "http://8080-f323e70d-cd11-4a02-a245-6b4ed2ce1e31.isodo.localhost/.well-known/t3/environment", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "GET")
		r.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if origin == "http://app.isodo.localhost:5734" {
			if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatalf("preflight status=%d headers=%v", w.Code, w.Header())
			}
		} else if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("foreign origin allowed: %d", w.Code)
		}
	}
	// A successful preflight must not authorize the resource itself.
	r := httptest.NewRequest(http.MethodGet, "http://8080-f323e70d-cd11-4a02-a245-6b4ed2ce1e31.isodo.localhost/.well-known/t3/environment", nil)
	r.Header.Set("Origin", "http://app.isodo.localhost:5734")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status=%d", w.Code)
	}
}
