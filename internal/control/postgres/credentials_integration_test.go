package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

type testBearer map[string]auth.Identity

func (a testBearer) Authenticate(_ context.Context, header string) (auth.Identity, error) {
	if i, ok := a[header]; ok {
		return i, nil
	}
	return auth.Identity{}, auth.ErrUnauthenticated
}

func TestPostgresCredentialsLifecycle(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires isolated PostgreSQL database with both migrations")
	}
	ctx := t.Context()
	store, err := New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	issuer := fmt.Sprintf("https://credentials-%d.example.test", time.Now().UnixNano())
	tenantA, err := store.ResolveTenant(ctx, issuer, "a", "A")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := store.ResolveTenant(ctx, issuer, "b", "B")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTestTenants(t, databaseURL, tenantA, tenantB)
	bearer := testBearer{"Bearer a": {TenantID: tenantA}, "Bearer b": {TenantID: tenantB}}
	access := &auth.Access{Bearer: bearer, Keys: store}
	server := httptest.NewServer(httpapi.NewHandler(nil, auth.NewAPIKeyHandler(store), sandbox.NewHandler(store), access))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Error(err)
			return httptest.NewRecorder()
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		response, err := client.Do(r)
		if err != nil {
			t.Error(err)
			return w
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		if _, err := io.Copy(w.Body, response.Body); err != nil {
			t.Error(err)
		}
		return w
	}
	create := func(scopes string) (auth.APIKey, string) {
		t.Helper()
		w := request("POST", "/v1/api-keys", `{"name":"ci","scopes":`+scopes+`}`, map[string]string{"Authorization": "Bearer a"})
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			auth.APIKey
			Secret string `json:"api_key"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(result.Secret, "iso_"+result.ID+"_") {
			t.Fatal("invalid secret format")
		}
		return result.APIKey, result.Secret
	}
	key, raw := create(`["sandboxes:read","sandboxes:write"]`)
	readOnly, readRaw := create(`["sandboxes:read"]`)
	_ = readOnly
	list := request("GET", "/v1/api-keys", "", map[string]string{"Authorization": "Bearer a"})
	if list.Code != 200 || strings.Contains(list.Body.String(), raw) || strings.Contains(list.Body.String(), "api_key\"") || strings.Contains(list.Body.String(), "secret_hash") {
		t.Fatal("key list leaked a credential")
	}
	otherList := request("GET", "/v1/api-keys", "", map[string]string{"Authorization": "Bearer b"})
	if strings.Contains(otherList.Body.String(), key.ID) {
		t.Fatal("cross tenant key list")
	}
	authHeader := map[string]string{"X-API-Key": raw}
	// Registry fixture only; this test checks API-key authorization and pinning,
	// not execution of a real Guest or integrity of S3 artifacts.
	publication, err := store.RegisterSystemTemplate(ctx, "linux-base", "test fixture", strings.Repeat("a", 64), "test-bucket", "")
	if err != nil {
		t.Fatal(err)
	}
	created := request("POST", "/v1/sandboxes", `{"name":"owned"}`, authHeader)
	if created.Code != 201 {
		t.Fatalf("key create sandbox: %d %s", created.Code, created.Body.String())
	}
	var owned sandbox.Sandbox
	_ = json.Unmarshal(created.Body.Bytes(), &owned)
	if owned.TemplateBuildID == nil || *owned.TemplateBuildID != publication.BuildID {
		t.Fatal("API-key creation did not pin the default template build")
	}
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		w := request(method, "/v1/sandboxes/"+owned.ID, `{"name":"other"}`, map[string]string{"Authorization": "Bearer b"})
		if w.Code != 404 {
			t.Fatalf("cross tenant %s: %d", method, w.Code)
		}
	}
	for _, check := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/sandboxes", "", 200},
		{"GET", "/v1/sandboxes/" + owned.ID, "", 200},
		{"PATCH", "/v1/sandboxes/" + owned.ID, `{"name":"updated"}`, 200},
		{"DELETE", "/v1/sandboxes/" + owned.ID, "", 204},
	} {
		if w := request(check.method, check.path, check.body, authHeader); w.Code != check.status {
			t.Fatalf("API key HTTP %s: status %d want %d", check.method, w.Code, check.status)
		}
	}
	if w := request("POST", "/v1/sandboxes", `{"name":"forbidden"}`, map[string]string{"X-API-Key": readRaw}); w.Code != 403 {
		t.Fatal("read key allowed mutation")
	}
	if w := request("HEAD", "/v1/sandboxes", "", map[string]string{"X-API-Key": readRaw}); w.Code != 200 {
		t.Fatal("read key rejected HEAD")
	}
	if w := request("GET", "/v1/api-keys", "", authHeader); w.Code != 401 {
		t.Fatal("API key allowed key management")
	}
	if w := request("GET", "/v1/sandboxes", "", map[string]string{"X-API-Key": raw, "Authorization": "Bearer a"}); w.Code != 401 {
		t.Fatal("ambiguous credentials accepted")
	}
	r := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	r.Header.Add("X-API-Key", raw)
	r.Header.Add("X-API-Key", raw)
	w := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("duplicate API key header accepted")
	}
	if w := request("POST", "/v1/api-keys", `{"name":"ci","scopes":["sandboxes:read"],"tenant_id":"`+tenantB+`"}`, map[string]string{"Authorization": "Bearer a"}); w.Code != 400 {
		t.Fatal("unknown fields accepted")
	}
	if w := request("DELETE", "/v1/api-keys/"+key.ID, "", map[string]string{"Authorization": "Bearer b"}); w.Code != 404 {
		t.Fatal("cross tenant key revoke")
	}
	if w := request("DELETE", "/v1/api-keys/"+key.ID, "", map[string]string{"Authorization": "Bearer a"}); w.Code != 204 {
		t.Fatal("owner revoke failed")
	}
	if w := request("GET", "/v1/sandboxes", "", authHeader); w.Code != 401 {
		t.Fatal("revoked key accepted")
	}
	// Expiry is enforced by the database, independent of Casdoor availability.
	if _, err := store.pool.Exec(ctx, `UPDATE api_keys SET expires_at=now()-interval '1 second' WHERE id=$1`, readOnly.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/v1/sandboxes", "", map[string]string{"X-API-Key": readRaw}); w.Code != 401 {
		t.Fatal("expired key accepted")
	}
	// Nine of twenty concurrent creations can succeed: one unrevoked key exists.
	var wg sync.WaitGroup
	codes := make([]int, 20)
	for i := range codes {
		wg.Go(func() {
			codes[i] = request("POST", "/v1/api-keys", `{"name":"parallel","scopes":["sandboxes:read"]}`, map[string]string{"Authorization": "Bearer a"}).Code
		})
	}
	wg.Wait()
	success := 0
	for _, code := range codes {
		if code == 201 {
			success++
		} else if code != 409 {
			t.Fatalf("unexpected limit response: %d", code)
		}
	}
	if success != 9 {
		t.Fatalf("concurrent key limit: got %d creations want 9", success)
	}

	hash := sha256.Sum256([]byte("test-session"))
	session := auth.Session{Identity: auth.Identity{TenantID: tenantA}, CSRF: "csrf", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.SaveSession(ctx, hash[:], session); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(ctx, hash[:])
	if err != nil || got.Identity.Subject != "a" || got.Identity.TenantID != tenantA {
		t.Fatal("session identity mapping failed")
	}
	if err := store.DeleteSession(ctx, hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(ctx, hash[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("deleted session accepted")
	}
	state := auth.OAuthState{Nonce: "nonce", Verifier: "verifier"}
	if err := store.SaveOAuthState(ctx, hash[:], state, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	gotState, err := store.ConsumeOAuthState(ctx, hash[:])
	if err != nil || gotState != state {
		t.Fatal("OAuth state consumption failed")
	}
	if _, err := store.ConsumeOAuthState(ctx, hash[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("OAuth state reusable")
	}
	if err := store.SaveOAuthState(ctx, hash[:], state, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeOAuthState(ctx, hash[:]); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("expired state accepted")
	}
	var secretHash []byte
	if err := store.pool.QueryRow(ctx, `SELECT secret_hash FROM api_keys WHERE id=$1`, key.ID).Scan(&secretHash); err != nil {
		t.Fatal(err)
	}
	if len(secretHash) != 32 || bytes.Contains(secretHash, []byte(raw)) {
		t.Fatal("credential storage not a hash")
	}
}
