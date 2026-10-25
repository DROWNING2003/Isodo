package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/preview"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func (s *memoryStore) StartSandbox(_ context.Context, tenantID, id string) (sandbox.Sandbox, error) {
	item, ok := s.items[id]
	if !ok || item.TenantID != tenantID {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	if item.DesiredState != "stopped" || (item.ObservedState != "stopped" && item.ObservedState != "error") {
		return sandbox.Sandbox{}, sandbox.ErrConflict
	}
	item.DesiredState, item.ObservedState = "running", "requested"
	s.items[id] = item
	return item, nil
}

func (s *memoryStore) StopSandbox(_ context.Context, tenantID, id string) (sandbox.Sandbox, error) {
	item, ok := s.items[id]
	if !ok || item.TenantID != tenantID {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	if item.DesiredState != "running" || item.ObservedState != "running" {
		return sandbox.Sandbox{}, sandbox.ErrConflict
	}
	item.DesiredState, item.ObservedState = "stopped", "requested"
	s.items[id] = item
	return item, nil
}

func (s *memoryStore) DeleteSandbox(_ context.Context, tenantID, id string) (sandbox.Sandbox, error) {
	item, ok := s.items[id]
	if !ok || item.TenantID != tenantID {
		return sandbox.Sandbox{}, sandbox.ErrNotFound
	}
	delete(s.items, id)
	return item, nil
}

type fakeAppLink struct {
	url     string
	expires time.Time
	err     error
}

func (f fakeAppLink) CreateSandboxAppLink(context.Context, string) (string, time.Time, error) {
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.url, f.expires, nil
}

func TestSandboxAppLinkReturnsJSON(t *testing.T) {
	store := newMemoryStore()
	expires := time.Now().Add(5 * time.Minute)
	appURL := "http://8080-" + otherSandboxID + ".localhost:43123/pair#token=secret"
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store, fakeAppLink{url: appURL, expires: expires}), &auth.Access{Bearer: testAuthenticator{}})

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+otherSandboxID+"/app-link", nil)
	req.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var link publicapi.SandboxAppLink
	if err := json.Unmarshal(response.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode app link: %v", err)
	}
	if link.SandboxId != otherSandboxID || link.Service != publicapi.SandboxAppLinkService("t3") || link.Status != publicapi.SandboxAppLinkStatusReady {
		t.Fatalf("unexpected app link: %#v", link)
	}
	if link.Url != "http://8080-"+otherSandboxID+".localhost:43123/" {
		t.Fatalf("base URL = %q", link.Url)
	}
	if link.PairingUrl != appURL {
		t.Fatalf("pairing URL = %q", link.PairingUrl)
	}
	if link.Generation != nil {
		t.Fatalf("generation = %q, want omitted", *link.Generation)
	}
	if link.Capabilities == nil || len(*link.Capabilities) != 2 || (*link.Capabilities)[0] != "terminal" || (*link.Capabilities)[1] != "files" {
		t.Fatalf("capabilities = %#v", link.Capabilities)
	}
	if !link.ExpiresAt.Equal(expires) {
		t.Fatalf("expires_at = %s, want %s", link.ExpiresAt, expires)
	}
}

type fakePublisher struct {
	base    string
	pairing string
}

func (f fakePublisher) Publish(string, uint16, string) (string, string, error) {
	return f.base, f.pairing, nil
}

func TestSandboxAppLinkUsesPublicPreviewHost(t *testing.T) {
	store := newMemoryStore()
	appURL := "http://8080-" + otherSandboxID + ".localhost:43123/pair#token=secret"
	baseURL := "http://8080-" + otherSandboxID + ".localhost:43123/"
	pairingURL := "http://8080-" + otherSandboxID + ".localhost:43123/__isodo/auth/token?next=/pair#token=secret"
	handler := sandbox.NewHandler(store, fakeAppLink{url: appURL, expires: time.Now().Add(5 * time.Minute)})
	handler.SetAppLinkPublisher(fakePublisher{base: baseURL, pairing: pairingURL})
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: testAuthenticator{}})

	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+otherSandboxID+"/app-link", nil)
	request.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var link publicapi.SandboxAppLink
	if err := json.Unmarshal(response.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.Url != baseURL || link.PairingUrl != pairingURL {
		t.Fatalf("link = %#v", link)
	}

	redirect := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/"+otherSandboxID+"/app", nil)
	redirect.Header.Set("Authorization", "Bearer user-a")
	redirected := httptest.NewRecorder()
	mux.ServeHTTP(redirected, redirect)
	if redirected.Code != http.StatusSeeOther || redirected.Header().Get("Location") != pairingURL {
		t.Fatalf("redirect status=%d location=%q", redirected.Code, redirected.Header().Get("Location"))
	}
}

func TestSandboxAppLinkAndPreviewGatewayEndToEnd(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "8080-"+otherSandboxID+".localhost:43123" {
			t.Errorf("upstream host = %q", r.Host)
		}
		_, _ = w.Write([]byte("t3-terminal-files"))
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	publisher, err := preview.New(preview.Config{Secret: "preview-test", Domain: "localhost", Scheme: "http", APIPort: "43123"})
	if err != nil {
		t.Fatal(err)
	}
	nodeURL := "http://8080-" + otherSandboxID + ".localhost:" + backendURL.Port() + "/pair#token=pair-token"
	handler := sandbox.NewHandler(newMemoryStore(), fakeAppLink{url: nodeURL, expires: time.Now().Add(5 * time.Minute)})
	handler.SetAppLinkPublisher(publisher)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: testAuthenticator{}})
	gateway := publisher.Wrap(mux)

	linkRequest := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+otherSandboxID+"/app-link", nil)
	linkRequest.Header.Set("Authorization", "Bearer user-a")
	linkResponse := httptest.NewRecorder()
	gateway.ServeHTTP(linkResponse, linkRequest)
	if linkResponse.Code != http.StatusOK {
		t.Fatalf("app-link status=%d body=%s", linkResponse.Code, linkResponse.Body.String())
	}
	var link publicapi.SandboxAppLink
	if err := json.Unmarshal(linkResponse.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link.PairingUrl)
	if err != nil || parsed.Host != "8080-"+otherSandboxID+".localhost:43123" || parsed.Fragment != "token=pair-token" {
		t.Fatalf("pairing_url = %q (%v)", link.PairingUrl, err)
	}

	authRequest := httptest.NewRequest(http.MethodGet, "http://"+parsed.Host+parsed.EscapedPath()+"?next=/pair", nil)
	authResponse := httptest.NewRecorder()
	gateway.ServeHTTP(authResponse, authRequest)
	if authResponse.Code != http.StatusFound || authResponse.Header().Get("Location") != "/pair" {
		t.Fatalf("auth status=%d location=%q", authResponse.Code, authResponse.Header().Get("Location"))
	}
	cookies := authResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}

	appRequest := httptest.NewRequest(http.MethodGet, "http://"+parsed.Host+"/", nil)
	appRequest.AddCookie(cookies[0])
	appResponse := httptest.NewRecorder()
	gateway.ServeHTTP(appResponse, appRequest)
	if appResponse.Code != http.StatusOK || appResponse.Body.String() != "t3-terminal-files" {
		t.Fatalf("preview status=%d body=%q", appResponse.Code, appResponse.Body.String())
	}

	redirectRequest := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/"+otherSandboxID+"/app", nil)
	redirectRequest.Header.Set("Authorization", "Bearer user-a")
	redirectResponse := httptest.NewRecorder()
	gateway.ServeHTTP(redirectResponse, redirectRequest)
	redirect, err := url.Parse(redirectResponse.Header().Get("Location"))
	// Each issuance has its own expiry; crossing a second may legitimately change
	// the signed credential while keeping the same pairing destination.
	if err != nil || redirectResponse.Code != http.StatusSeeOther || redirect.Host != parsed.Host || redirect.Scheme != parsed.Scheme || redirect.RawQuery != parsed.RawQuery || redirect.Fragment != parsed.Fragment || !strings.HasPrefix(redirect.Path, "/__isodo/auth/") {
		t.Fatalf("app redirect did not preserve pairing destination: status=%d", redirectResponse.Code)
	}
	freshAuth := httptest.NewRequest(http.MethodGet, redirect.String(), nil)
	freshResponse := httptest.NewRecorder()
	gateway.ServeHTTP(freshResponse, freshAuth)
	if freshResponse.Code != http.StatusFound || len(freshResponse.Result().Cookies()) == 0 {
		t.Fatalf("redirect credential was not accepted: status=%d", freshResponse.Code)
	}
}

func TestSandboxAppLinkReportsUnsupportedBackend(t *testing.T) {
	store := newMemoryStore()
	appLink := fakeAppLink{err: fmt.Errorf("create T3 pairing link: %w", status.Error(codes.Unimplemented, "not implemented"))}
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store, appLink), &auth.Access{Bearer: testAuthenticator{}})

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+otherSandboxID+"/app-link", nil)
	req.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)

	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), `"SANDBOX_APP_UNSUPPORTED"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
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
			expected := http.StatusNotFound
			if request.method == http.MethodDelete {
				expected = http.StatusNoContent
			}
			if response.Code != expected || (expected == http.StatusNotFound && !strings.Contains(response.Body.String(), `"SANDBOX_NOT_FOUND"`)) {
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
		if created.Code != http.StatusAccepted || !strings.Contains(created.Body.String(), `"observed_state":"requested"`) {
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

func TestSandboxLifecycleEndpoints(t *testing.T) {
	store := newMemoryStore()
	stoppedID := "12d4c7e6-8f26-4f7a-9b5d-6f2d1c3e4a5b"
	runningID := "f7b1b6d0-0c0e-4d4e-9a0b-1d2c3e4f5a6b"
	store.items[stoppedID] = sandbox.Sandbox{ID: stoppedID, TenantID: "tenant-a", Name: "stopped", Metadata: json.RawMessage(`{}`), DesiredState: "stopped", ObservedState: "stopped"}
	store.items[runningID] = sandbox.Sandbox{ID: runningID, TenantID: "tenant-a", Name: "running", Metadata: json.RawMessage(`{}`), DesiredState: "running", ObservedState: "running"}
	mux := httpapi.NewHandler(nil, nil, sandbox.NewHandler(store), &auth.Access{Bearer: testAuthenticator{}})

	request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+stoppedID+"/start", nil)
	request.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start status %d body %s", response.Code, response.Body.String())
	}
	var item publicapi.Sandbox
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || item.DesiredState != "running" {
		t.Fatalf("start response %+v %v", item, err)
	}
	if _, err := store.StartSandbox(t.Context(), "tenant-a", stoppedID); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatalf("start should conflict while requested: %v", err)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+runningID+"/stop", nil)
	request.Header.Set("Authorization", "Bearer user-a")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("stop status %d body %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || item.DesiredState != "stopped" {
		t.Fatalf("stop response %+v %v", item, err)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+stoppedID+"/start", nil)
	request.Header.Set("Authorization", "Bearer user-b")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign start status %d", response.Code)
	}
}
