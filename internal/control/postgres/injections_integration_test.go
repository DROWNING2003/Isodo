package postgres

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/injection"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

func TestPostgresInjectionRuleLifecycle(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	ctx := t.Context()
	store, err := New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	issuer := fmt.Sprintf("https://injections-%d.test", time.Now().UnixNano())
	tenant, err := store.ResolveTenant(ctx, issuer, "injections", "Injections")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupTestTenants(t, databaseURL, tenant) })

	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	sealer, err := injection.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandlerWithInjections(nil, nil, sandbox.NewHandler(store),
		&auth.Access{Bearer: testBearer{"Bearer " + tenant: {TenantID: tenant, Credential: "session"}}},
		injection.NewHandler(injection.NewPostgresStore(store.Pool()), sealer))
	request := func(method, path, body string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tenant)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s = %d body = %s", method, path, w.Code, w.Body.String())
		}
		var decoded map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &decoded)
		return decoded
	}

	created := request(http.MethodPost, "/v1/injection-rules", `{
		"name":"personal-github","type":"github","secret":"ghp_live_secret"
	}`, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %v", created)
	}
	if strings.Contains(mustJSON(t, created), "ghp_live_secret") {
		t.Fatal("create response leaked the secret")
	}
	list := request(http.MethodGet, "/v1/injection-rules", "", http.StatusOK)
	if list["total"].(float64) != 1 {
		t.Fatalf("list = %v", list)
	}
	material := request(http.MethodGet, "/v1/injection-rules/"+id+"/material", "", http.StatusOK)
	if material["token"] != "ghp_live_secret" {
		t.Fatalf("material = %v", material)
	}
	request(http.MethodPatch, "/v1/injection-rules/"+id, `{"name":"personal-github-2"}`, http.StatusOK)
	material = request(http.MethodGet, "/v1/injection-rules/"+id+"/material", "", http.StatusOK)
	if material["token"] != "ghp_live_secret" {
		t.Fatalf("patch without secret changed material: %v", material)
	}
	request(http.MethodDelete, "/v1/injection-rules/"+id, "", http.StatusNoContent)
	request(http.MethodGet, "/v1/injection-rules/"+id, "", http.StatusNotFound)
	mcp := request(http.MethodPost, "/v1/injection-rules", `{"name":"mcp-service","type":"mcp","base_url":"https://mcp.example.com/mcp","secret":"fixture-token"}`, http.StatusCreated)
	mcpID := mcp["id"].(string)
	stored := request(http.MethodGet, "/v1/injection-rules/"+mcpID, "", http.StatusOK)
	if stored["type"] != "mcp" || strings.Contains(mustJSON(t, stored), "fixture-token") {
		t.Fatal("MCP persistence view is incorrect")
	}
	mcpMaterial := request(http.MethodGet, "/v1/injection-rules/"+mcpID+"/material", "", http.StatusOK)
	if mcpMaterial["headers"].(map[string]any)["Authorization"] != "Bearer fixture-token" {
		t.Fatal("MCP material round trip failed")
	}
	request(http.MethodDelete, "/v1/injection-rules/"+mcpID, "", http.StatusNoContent)

}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
