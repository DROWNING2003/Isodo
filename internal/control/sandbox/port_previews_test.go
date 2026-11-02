package sandbox_test

import (
	"context"
	"fmt"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"net/http/httptest"
	"strings"
	"testing"
)

func (s *memoryStore) LookupPreviewSandbox(ctx context.Context, id string) (sandbox.Sandbox, error) {
	item, ok := s.items[id]
	if !ok || item.Deletion != nil {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	return item, nil
}
func (s *memoryStore) GetPortPreview(ctx context.Context, tenant, id string, port uint16) (preview.PortPolicy, error) {
	if _, err := s.GetSandbox(ctx, tenant, id); err != nil {
		return preview.PortPolicy{}, err
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	p, ok := s.policies[fmt.Sprintf("%s:%d", id, port)]
	if !ok {
		return preview.PortPolicy{Visibility: "public"}, nil
	}
	return p, nil
}
func (s *memoryStore) SetPortPreview(ctx context.Context, tenant, id string, port uint16, visibility string) (preview.PortPolicy, error) {
	if _, err := s.GetSandbox(ctx, tenant, id); err != nil {
		return preview.PortPolicy{}, err
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if s.policies == nil {
		s.policies = make(map[string]preview.PortPolicy)
	}
	key := fmt.Sprintf("%s:%d", id, port)
	p := s.policies[key]
	if p.Visibility != visibility {
		p.Revision++
	}
	p.Visibility = visibility
	s.policies[key] = p
	return p, nil
}

func TestPortVisibilityAPIOwnershipAndScopes(t *testing.T) {
	store := newMemoryStore()
	handler := sandbox.NewHandler(store)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: previewTestAuth{}})
	path := "/v1/sandboxes/" + otherSandboxID + "/previews/3000"
	for _, tc := range []struct {
		method, body, token string
		want                int
	}{
		{"GET", "", "Bearer user-a", 200},
		{"PUT", `{"visibility":"private"}`, "Bearer writer", 403},
		{"PUT", `{"visibility":"private"}`, "Bearer user-b", 404},
		{"PUT", `{"visibility":"private"}`, "Bearer preview-key", 200},
		{"PUT", `{"visibility":"world"}`, "Bearer user-a", 400},
		{"GET", "", "Bearer user-b", 404},
	} {
		r := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", tc.token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.token, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer user-a")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"visibility":"private"`) {
		t.Fatal(w.Body.String())
	}
	for _, port := range []string{"31024", "31080", "38080"} {
		r := httptest.NewRequest("PUT", strings.Replace(path, "3000", port, 1), strings.NewReader(`{"visibility":"public"}`))
		r.Header.Set("Authorization", "Bearer user-a")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("reserved port %s=%d", port, w.Code)
		}
	}
}
