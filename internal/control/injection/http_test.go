package injection

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
)

type memoryStore struct {
	mu    sync.Mutex
	rules map[string]Rule
}

func newMemoryStore() *memoryStore { return &memoryStore{rules: map[string]Rule{}} }

func (s *memoryStore) List(_ context.Context, tenantID string) ([]Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []Rule
	for _, rule := range s.rules {
		if rule.TenantID == tenantID {
			result = append(result, rule)
		}
	}
	return result, nil
}

func (s *memoryStore) Get(_ context.Context, tenantID, id string) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.rules[id]
	if !ok || rule.TenantID != tenantID {
		return Rule{}, ErrNotFound
	}
	return rule, nil
}

func (s *memoryStore) Create(_ context.Context, rule Rule) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.rules {
		if existing.TenantID == rule.TenantID && existing.Name == rule.Name {
			return Rule{}, ErrExists
		}
	}
	rule.ID = "00000000-0000-0000-0000-00000000000" + string(rune('1'+len(s.rules)%8))
	s.rules[rule.ID] = rule
	return rule, nil
}

func (s *memoryStore) Update(_ context.Context, rule Rule) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.rules[rule.ID]
	if !ok || existing.TenantID != rule.TenantID {
		return Rule{}, ErrNotFound
	}
	for id, candidate := range s.rules {
		if id != rule.ID && candidate.TenantID == rule.TenantID && candidate.Name == rule.Name {
			return Rule{}, ErrExists
		}
	}
	s.rules[rule.ID] = rule
	return rule, nil
}

func (s *memoryStore) Delete(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, ok := s.rules[id]
	if !ok || rule.TenantID != tenantID {
		return ErrNotFound
	}
	delete(s.rules, id)
	return nil
}

type fixedAuth struct {
	identity auth.Identity
}

func (a fixedAuth) Authenticate(context.Context, string) (auth.Identity, error) {
	return a.identity, nil
}

func harness(t *testing.T, identity auth.Identity) (*httptest.Server, *memoryStore) {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	sealer, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	handler := NewHandler(store, sealer)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/injection-rules", handler.ListInjectionRules)
	mux.HandleFunc("POST /v1/injection-rules", handler.CreateInjectionRule)
	mux.HandleFunc("GET /v1/injection-rules/{ruleID}", withID(handler.GetInjectionRule))
	mux.HandleFunc("PATCH /v1/injection-rules/{ruleID}", withID(handler.UpdateInjectionRule))
	mux.HandleFunc("DELETE /v1/injection-rules/{ruleID}", withID(handler.DeleteInjectionRule))
	mux.HandleFunc("GET /v1/injection-rules/{ruleID}/material", withID(handler.GetInjectionRuleMaterial))
	server := httptest.NewServer(auth.Middleware(fixedAuth{identity: identity}, mux))
	t.Cleanup(server.Close)
	return server, store
}

func withID(handler func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, r.PathValue("ruleID"))
	}
}

func request(t *testing.T, server *httptest.Server, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response, decoded
}

var tenant = auth.Identity{TenantID: "tenant-a", Credential: "session"}

func TestCreateListMaterialAndUpdate(t *testing.T) {
	server, _ := harness(t, tenant)
	response, created := request(t, server, http.MethodPost, "/v1/injection-rules",
		`{"name":"github","type":"github","secret":"ghp_secret_value"}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body = %v", response.StatusCode, created)
	}
	if strings.Contains(string(mustJSON(t, created)), "ghp_secret_value") {
		t.Fatal("create response leaked the secret")
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %v", created)
	}

	response, list := request(t, server, http.MethodGet, "/v1/injection-rules", "")
	if response.StatusCode != http.StatusOK || list["total"].(float64) != 1 {
		t.Fatalf("list status = %d body = %v", response.StatusCode, list)
	}

	response, material := request(t, server, http.MethodGet, "/v1/injection-rules/"+id+"/material", "")
	if response.StatusCode != http.StatusOK || material["token"] != "ghp_secret_value" {
		t.Fatalf("material status = %d body = %v", response.StatusCode, material)
	}

	response, updated := request(t, server, http.MethodPatch, "/v1/injection-rules/"+id,
		`{"name":"github-prod"}`)
	if response.StatusCode != http.StatusOK || updated["name"] != "github-prod" {
		t.Fatalf("update status = %d body = %v", response.StatusCode, updated)
	}
	_, material = request(t, server, http.MethodGet, "/v1/injection-rules/"+id+"/material", "")
	if material["token"] != "ghp_secret_value" {
		t.Fatalf("update without secret changed material: %v", material)
	}

	response, _ = request(t, server, http.MethodPatch, "/v1/injection-rules/"+id,
		`{"secret":"ghp_new_value"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update secret status = %d", response.StatusCode)
	}
	_, material = request(t, server, http.MethodGet, "/v1/injection-rules/"+id+"/material", "")
	if material["token"] != "ghp_new_value" {
		t.Fatalf("update did not replace material: %v", material)
	}

	response, _ = request(t, server, http.MethodDelete, "/v1/injection-rules/"+id, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
}

func TestValidationAndConflict(t *testing.T) {
	server, _ := harness(t, tenant)
	response, _ := request(t, server, http.MethodPost, "/v1/injection-rules",
		`{"name":"bad","type":"http","secret":"x"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing base URL status = %d", response.StatusCode)
	}
	response, _ = request(t, server, http.MethodPost, "/v1/injection-rules",
		`{"name":"http","type":"http","base_url":"https://api.example.com","header_name":"X-Key","secret":"abc"}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", response.StatusCode)
	}
	response, _ = request(t, server, http.MethodPost, "/v1/injection-rules",
		`{"name":"http","type":"http","base_url":"https://api.example.com","secret":"abc"}`)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d", response.StatusCode)
	}
}

func TestAPIKeyCannotReadMaterial(t *testing.T) {
	server, _ := harness(t, auth.Identity{
		TenantID: "tenant-a", Credential: "api_key", Scopes: []string{"sandboxes:read"},
	})
	response, body := request(t, server, http.MethodGet, "/v1/injection-rules", "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("api key list status = %d body = %v", response.StatusCode, body)
	}
}

func TestSealerRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	sealer, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, err := sealer.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := sealer.Open(nonce, sealed)
	if err != nil || string(opened) != "secret" {
		t.Fatalf("open = %q err = %v", opened, err)
	}
	other, err := NewSealer(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(nonce, sealed); err == nil {
		t.Fatal("wrong key decrypted the secret")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestLoadKeyGeneratesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "injection.key")
	first, err := LoadKey("", path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadKey("", path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || len(first) != 32 {
		t.Fatal("generated key did not reload")
	}
	if _, err := LoadKey("not-base64", ""); err == nil {
		t.Fatal("invalid explicit key was accepted")
	}
}

func TestMCPCredentialLifecycle(t *testing.T) {
	server, _ := harness(t, tenant)
	response, _ := request(t, server, http.MethodPost, "/v1/injection-rules", `{"name":"invalid-mcp","type":"mcp","secret":"test-secret"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing MCP URL status = %d", response.StatusCode)
	}
	response, created := request(t, server, http.MethodPost, "/v1/injection-rules", `{"name":"mcp","type":"mcp","base_url":"https://mcp.example.com/mcp","secret":"test-secret-value"}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", response.StatusCode)
	}
	if created["type"] != "mcp" || strings.Contains(string(mustJSON(t, created)), "test-secret-value") {
		t.Fatal("invalid MCP view")
	}
	id := created["id"].(string)
	response, _ = request(t, server, http.MethodPatch, "/v1/injection-rules/"+id, `{"name":"renamed-mcp"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", response.StatusCode)
	}
	_, material := request(t, server, http.MethodGet, "/v1/injection-rules/"+id+"/material", "")
	if material["headers"].(map[string]any)["Authorization"] != "Bearer test-secret-value" {
		t.Fatal("MCP bearer material not retained")
	}
	response, _ = request(t, server, http.MethodDelete, "/v1/injection-rules/"+id, "")
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	for _, test := range []struct{ header, secret, want string }{
		{"Authorization", "Bearer token", "Bearer token"},
		{"X-API-Key", "token", "token"},
	} {
		if BuildMaterial(TypeMCP, test.header, test.secret).Headers[test.header] != test.want {
			t.Fatal("incorrect MCP header")
		}
	}
}
