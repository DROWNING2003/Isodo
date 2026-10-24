package sandbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

const otherSandboxID = "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"

type testAuthenticator struct{}

func (testAuthenticator) Authenticate(_ context.Context, authorization string) (auth.Identity, error) {
	switch authorization {
	case "Bearer user-a":
		return auth.Identity{TenantID: "tenant-a"}, nil
	case "Bearer user-b":
		return auth.Identity{TenantID: "tenant-b"}, nil
	default:
		return auth.Identity{}, auth.ErrUnauthenticated
	}
}

type memoryStore struct {
	items map[string]sandbox.Sandbox
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: map[string]sandbox.Sandbox{
		otherSandboxID: {
			ID: otherSandboxID, TenantID: "tenant-a", Name: "private", Metadata: json.RawMessage(`{}`),
			DesiredState: "stopped", ObservedState: "requested", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
		},
	}}
}

func (s *memoryStore) CreateSandbox(_ context.Context, tenantID, name string, metadata json.RawMessage, templateRef string) (sandbox.Sandbox, error) {
	item := sandbox.Sandbox{ID: "eec8e2b0-b8a0-4b30-b292-7257e44b83b4", TenantID: tenantID, Name: name, Metadata: metadata, DesiredState: "stopped", ObservedState: "requested"}
	s.items[item.ID] = item
	return item, nil
}

func (s *memoryStore) ListSandboxes(_ context.Context, tenantID string, limit, offset int) ([]sandbox.Sandbox, int64, error) {
	items := make([]sandbox.Sandbox, 0)
	for _, item := range s.items {
		if item.TenantID == tenantID {
			items = append(items, item)
		}
	}
	if offset >= len(items) {
		return []sandbox.Sandbox{}, int64(len(items)), nil
	}
	end := min(offset+limit, len(items))
	return items[offset:end], int64(len(items)), nil
}

func (s *memoryStore) GetSandbox(_ context.Context, tenantID, id string) (sandbox.Sandbox, error) {
	item, ok := s.items[id]
	if !ok || item.TenantID != tenantID {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	return item, nil
}

func (s *memoryStore) UpdateSandbox(_ context.Context, tenantID, id string, patch sandbox.Patch) (sandbox.Sandbox, error) {
	item, err := s.GetSandbox(context.Background(), tenantID, id)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	if patch.Name != nil {
		item.Name = *patch.Name
	}
	if patch.Metadata != nil {
		item.Metadata = *patch.Metadata
	}
	s.items[id] = item
	return item, nil
}

func (s *memoryStore) DeleteSandbox(_ context.Context, tenantID, id string) error {
	item, ok := s.items[id]
	if !ok || item.TenantID != tenantID {
		return sandbox.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func TestSandboxCRUDIsTenantScoped(t *testing.T) {
	store := newMemoryStore()
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store), &auth.Access{Bearer: testAuthenticator{}})

	t.Run("other tenant cannot get, patch, or delete", func(t *testing.T) {
		for _, request := range []struct{ method, path, body string }{
			{http.MethodGet, "/v1/sandboxes/" + otherSandboxID, ""},
			{http.MethodPatch, "/v1/sandboxes/" + otherSandboxID, `{"name":"stolen"}`},
			{http.MethodDelete, "/v1/sandboxes/" + otherSandboxID, ""},
		} {
			req := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
			req.Header.Set("Authorization", "Bearer user-b")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"SANDBOX_NOT_FOUND"`) {
				t.Errorf("%s %s: status=%d body=%s", request.method, request.path, response.Code, response.Body.String())
			}
		}
		if store.items[otherSandboxID].Name != "private" {
			t.Fatalf("foreign sandbox was modified: %#v", store.items[otherSandboxID])
		}
	})

	t.Run("list is limited to the authenticated tenant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
		req.Header.Set("Authorization", "Bearer user-b")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), otherSandboxID) {
			t.Fatalf("list response = %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("client cannot choose tenant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(`{"name":"new","tenant_id":"tenant-a"}`))
		req.Header.Set("Authorization", "Bearer user-b")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("create with tenant_id returned %d: %s", response.Code, response.Body.String())
		}
	})

	t.Run("owner can create and read metadata", func(t *testing.T) {
		create := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(`{"name":"dev","metadata":{"purpose":"test"}}`))
		create.Header.Set("Authorization", "Bearer user-a")
		created := httptest.NewRecorder()
		mux.ServeHTTP(created, create)
		if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"observed_state":"requested"`) {
			t.Fatalf("create response = %d %s", created.Code, created.Body.String())
		}
	})
}

func TestSandboxReturnsSameNotFoundForMissingAndForeignIDs(t *testing.T) {
	store := newMemoryStore()
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store), &auth.Access{Bearer: testAuthenticator{}})
	responses := make([]string, 0, 2)
	for _, id := range []string{otherSandboxID, "f323e70d-cd11-4a02-a245-000000000000"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/"+id, nil)
		req.Header.Set("Authorization", "Bearer user-b")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		responses = append(responses, response.Body.String())
	}
	if responses[0] != responses[1] {
		t.Fatalf("foreign/missing responses differ: %q != %q", responses[0], responses[1])
	}
}

func TestMalformedSandboxIDDoesNotReachStore(t *testing.T) {
	store := newMemoryStore()
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store), &auth.Access{Bearer: testAuthenticator{}})
	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestSandboxRoutesRequireExactlyOneBearerHeader(t *testing.T) {
	store := newMemoryStore()
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store), &auth.Access{Bearer: testAuthenticator{}})
	for _, headers := range [][]string{nil, {"Bearer user-a", "Bearer user-b"}} {
		req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
		for _, value := range headers {
			req.Header.Add("Authorization", value)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("Authorization headers %v: status=%d", headers, response.Code)
		}
		if response.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("Authorization headers %v: missing WWW-Authenticate", headers)
		}
	}
}

var _ sandbox.Store = (*memoryStore)(nil)
