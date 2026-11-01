package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebHostSelectionIgnoresForwardedHost(t *testing.T) {
	handler := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	}
	h := routeWebHosts(handler(204), map[string]http.Handler{"web.drowning.top:18443": handler(201)})
	for _, tt := range []struct {
		host string
		want int
	}{{"web.drowning.top:18443", 201}, {"api.drowning.top:18443", 204}, {"evil.example", 204}} {
		r := httptest.NewRequest("GET", "https://"+tt.host+"/auth/login", nil)
		r.Header.Set("X-Forwarded-Host", "web.drowning.top:18443")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("host=%s status=%d", tt.host, w.Code)
		}
	}
}
