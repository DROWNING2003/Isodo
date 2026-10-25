package injection

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
)

type documentMemory struct {
	*memoryStore
	docs map[string]SealedDocument
}

func (s *documentMemory) ListDocuments(_ context.Context, tenant, kind string) ([]SealedDocument, error) {
	result := []SealedDocument{}
	for key, item := range s.docs {
		if strings.HasPrefix(key, tenant+"/"+kind+"/") {
			result = append(result, item)
		}
	}
	return result, nil
}
func (s *documentMemory) PutDocument(_ context.Context, tenant, kind string, item SealedDocument) error {
	s.docs[tenant+"/"+kind+"/"+item.ID] = item
	return nil
}
func (s *documentMemory) DeleteDocument(_ context.Context, tenant, kind, id string) error {
	delete(s.docs, tenant+"/"+kind+"/"+id)
	return nil
}
func TestCredentialDocumentsAccountIsolation(t *testing.T) {
	store := &documentMemory{newMemoryStore(), map[string]SealedDocument{}}
	store.rules["connection-a"] = Rule{ID: "connection-a", TenantID: "tenant-a"}
	sealer, err := NewSealer(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, sealer)
	perform := func(tenant, credential, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/credential-documents/{kind}/{id}", handler.CredentialDocuments)
		mux.HandleFunc("/v1/credential-documents/{kind}", handler.CredentialDocuments)
		path := "/v1/credential-documents/agents"
		if method != "GET" {
			path += "/agent-a"
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test")
		result := httptest.NewRecorder()
		auth.Middleware(fixedAuth{identity: auth.Identity{TenantID: tenant, Credential: credential}}, mux).ServeHTTP(result, req)
		return result
	}
	body := `{"id":"agent-a","name":"Work","agent":"codex","connectionId":"connection-a","filePath":"~/.codex/config.toml","defaultModel":"relay-model","modelNames":["relay-model"],"content":"${ISODO_SECRET}"}`
	if result := perform("tenant-a", "session", "PUT", body); result.Code != 200 {
		t.Fatalf("save: %d %s", result.Code, result.Body)
	}
	if bytes.Contains(store.docs["tenant-a/agents/agent-a"].Ciphertext, []byte("relay-model")) {
		t.Fatal("configuration persisted as plaintext")
	}
	if result := perform("tenant-a", "session", "GET", ""); !strings.Contains(result.Body.String(), "relay-model") {
		t.Fatal("owner cannot reload saved model")
	}
	var list struct {
		Items []CredentialDocument `json:"items"`
	}
	_ = json.Unmarshal(perform("tenant-b", "session", "GET", "").Body.Bytes(), &list)
	if len(list.Items) != 0 {
		t.Fatal("cross-account read")
	}
	if result := perform("tenant-b", "session", "PUT", body); result.Code != 400 {
		t.Fatal("cross-account credential accepted")
	}
	if result := perform("tenant-a", "api_key", "GET", ""); result.Code != 403 {
		t.Fatal("sandbox key may read account configuration")
	}
	perform("tenant-b", "session", "DELETE", "")
	if len(store.docs) != 1 {
		t.Fatal("cross-account delete")
	}
	perform("tenant-a", "session", "DELETE", "")
	if len(store.docs) != 0 {
		t.Fatal("owner delete failed")
	}
}

func TestCredentialDocumentsPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("ISODO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated PostgreSQL")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var owner, other string
	if err := pool.QueryRow(t.Context(), "INSERT INTO tenants DEFAULT VALUES RETURNING id::text").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(t.Context(), "DELETE FROM tenants WHERE id=$1::uuid", owner)
	if err := pool.QueryRow(t.Context(), "INSERT INTO tenants DEFAULT VALUES RETURNING id::text").Scan(&other); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(t.Context(), "DELETE FROM tenants WHERE id=$1::uuid", other)
	sealer, _ := NewSealer(make([]byte, 32))
	nonce, sealed, err := sealer.Seal([]byte(`{"modelNames":["saved-model"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewPostgresStore(pool).PutDocument(t.Context(), owner, "agents", SealedDocument{"persisted", nonce, sealed}); err != nil {
		t.Fatal(err)
	}
	items, err := NewPostgresStore(pool).ListDocuments(t.Context(), owner, "agents")
	if err != nil || len(items) != 1 {
		t.Fatalf("reload: %v, count %d", err, len(items))
	}
	value, err := sealer.Open(items[0].Nonce, items[0].Ciphertext)
	if err != nil || !strings.Contains(string(value), "saved-model") {
		t.Fatal("persistent model missing")
	}
	others, err := NewPostgresStore(pool).ListDocuments(t.Context(), other, "agents")
	if err != nil || len(others) != 0 {
		t.Fatal("cross-account records")
	}
	if err := NewPostgresStore(pool).DeleteDocument(t.Context(), owner, "agents", "persisted"); err != nil {
		t.Fatal(err)
	}
	items, err = NewPostgresStore(pool).ListDocuments(t.Context(), owner, "agents")
	if err != nil || len(items) != 0 {
		t.Fatal("delete failed")
	}
}
