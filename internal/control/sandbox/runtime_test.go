package sandbox_test

import (
	"context"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type runtimeFake struct {
	request *nodev1.SetInjectionsRequest
	calls   int
}

func (*runtimeFake) NodeID() string { return "node-a" }
func (f *runtimeFake) GetInjections(context.Context, *nodev1.SandboxTarget) (*nodev1.SandboxInjections, error) {
	f.calls++
	return &nodev1.SandboxInjections{}, nil
}
func (f *runtimeFake) SetInjections(_ context.Context, r *nodev1.SetInjectionsRequest) (*nodev1.SandboxInjections, error) {
	f.calls++
	f.request = r
	return &nodev1.SandboxInjections{Enabled: true, Rules: []*nodev1.InjectionView{{Key: r.Rules[0].Key, Name: "model", Kind: "one-off", Spec: r.Rules[0].Spec, SecretHint: "masked"}}}, nil
}
func TestRuntimeInjectionHTTPAuthorizesOwnerAndNeverReturnsMaterial(t *testing.T) {
	store := newMemoryStore()
	item := store.items[otherSandboxID]
	item.NodeID = "node-a"
	item.OwnershipGeneration = 2
	item.Revision = "7"
	store.items[item.ID] = item
	runtime := &runtimeFake{}
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(runtime)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: testAuthenticator{}})
	body := `{"rules":[{"key":"one-off:model","name":"model","kind":"one-off","spec":{"type":"openai","base_url":"https://api.example.test/v1","if_headers":{"Authorization":"Bearer placeholder"}},"secret":{"api_key":"sensitive-model-api-key"}}]}`
	request := httptest.NewRequest(http.MethodPut, "/v1/sandboxes/"+item.ID+"/injections", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sensitive-model-api-key") || strings.Contains(response.Body.String(), `"secret":`) {
		t.Fatal("HTTP response exposed material")
	}
	if runtime.request.Target.OwnershipGeneration != 2 || runtime.request.Target.Revision != 7 || runtime.request.Rules[0].Secret.ApiKey != "sensitive-model-api-key" {
		t.Fatal("lost fenced target or material in internal request")
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		request = httptest.NewRequest(method, "/v1/sandboxes/"+item.ID+"/injections", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer user-b")
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != 404 || runtime.calls != 1 {
			t.Fatalf("foreign %s reached Node: status=%d calls=%d", method, response.Code, runtime.calls)
		}
	}
}

type runtimeKeyAuth struct{}

func (runtimeKeyAuth) Authenticate(context.Context, string) (auth.Identity, error) {
	return auth.Identity{TenantID: "tenant-a", Credential: "api_key", Scopes: []string{"sandboxes:read"}}, nil
}
func TestRuntimeInjectionRejectsSandboxAPIKeyBeforeNode(t *testing.T) {
	runtime := &runtimeFake{}
	handler := sandbox.NewHandler(newMemoryStore())
	handler.SetRuntime(runtime)
	route := auth.Middleware(runtimeKeyAuth{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.GetSandboxInjections(w, r, otherSandboxID) }))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer key")
	response := httptest.NewRecorder()
	route.ServeHTTP(response, request)
	if response.Code != 403 || runtime.calls != 0 || !strings.Contains(response.Body.String(), "SESSION_REQUIRED") {
		t.Fatal("sandbox API key reached credential RPC")
	}
}
