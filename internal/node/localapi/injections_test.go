package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/node/injection"
)

type fakeInjectionBackend struct {
	config injection.SandboxConfig
}

func (f *fakeInjectionBackend) SandboxInjections(string) injection.SandboxConfig {
	return f.config.View()
}

func (f *fakeInjectionBackend) SetSandboxInjections(_ context.Context, _ string, rules []injection.Rule) (injection.SandboxConfig, error) {
	config := injection.SandboxConfig{Rules: rules, Enabled: len(rules) > 0}
	f.config = config
	return config.View(), nil
}

func (f *fakeInjectionBackend) DeleteSandboxInjections(string) error {
	f.config = injection.SandboxConfig{}
	return nil
}

func (f *fakeInjectionBackend) InjectionSummary() map[string]int {
	return map[string]int{"rule-1": 2}
}

func (f *fakeInjectionBackend) InjectionCACertificate() []byte {
	return []byte("-----BEGIN CERTIFICATE-----\n")
}

func (f *fakeInjectionBackend) InjectionProxyPort(string) int { return 39000 }

func (f *fakeInjectionBackend) RemovePersistentInjection(string) error { return nil }

func (f *fakeInjectionBackend) InjectionStats(string) injection.Stats {
	return injection.Stats{Injected: 3, LastHost: "api.example.com"}
}

func TestLocalInjectionEndpoints(t *testing.T) {
	backend := &fakeInjectionBackend{}
	server := &Server{injections: backend}

	put := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/sandboxes/sandbox-a/injections", strings.NewReader(`{"rules":[{"key":"one-off:1","name":"临时","kind":"one-off","spec":{"type":"openai","base_url":"https://api.openai.com"},"secret":{"api_key":"sk-test"}}]}`))
	request.SetPathValue("sandboxID", "sandbox-a")
	server.putSandboxInjections(put, request)
	if put.Code != http.StatusOK {
		t.Fatalf("put status = %d body = %s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), "sk-test") {
		t.Fatal("local API leaked the secret")
	}

	get := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/sandboxes/sandbox-a/injections", nil)
	request.SetPathValue("sandboxID", "sandbox-a")
	server.getSandboxInjections(get, request)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d", get.Code)
	}
	var decoded injectionResponse
	if err := json.Unmarshal(get.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Rules) != 1 || decoded.Rules[0].Name != "临时" || decoded.Rules[0].Secret.APIKey != "" {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Stats.Injected != 3 {
		t.Fatalf("stats = %+v", decoded.Stats)
	}

	invalid := httptest.NewRecorder()
	tooMany := strings.Repeat(`{"key":"k","name":"n","kind":"one-off","spec":{"type":"openai"},"secret":{"api_key":"x"}},`, 33)
	request = httptest.NewRequest(http.MethodPut, "/sandboxes/sandbox-a/injections", strings.NewReader(`{"rules":[`+strings.TrimSuffix(tooMany, ",")+`]}`))
	request.SetPathValue("sandboxID", "sandbox-a")
	server.putSandboxInjections(invalid, request)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("too many rules status = %d", invalid.Code)
	}

	deleteResponse := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodDelete, "/sandboxes/sandbox-a/injections", nil)
	request.SetPathValue("sandboxID", "sandbox-a")
	server.deleteSandboxInjections(deleteResponse, request)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", deleteResponse.Code)
	}

	summary := httptest.NewRecorder()
	server.injectionSummary(summary, httptest.NewRequest(http.MethodGet, "/injections/summary", nil))
	if !strings.Contains(summary.Body.String(), `"rule-1":2`) {
		t.Fatalf("summary = %s", summary.Body.String())
	}
}
