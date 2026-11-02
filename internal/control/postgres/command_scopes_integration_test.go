package postgres

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/google/uuid"
)

func TestPostgresCommandKeyScopes(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("isolated migrated database required")
	}
	store, err := New(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tenant, err := store.ResolveTenant(t.Context(), "https://commands-"+uuid.NewString()+".test", "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTestTenants(t, databaseURL, tenant)
	for _, tc := range []struct {
		scopes []string
		want   int
	}{
		{[]string{"sandboxes:write"}, 403},
		{[]string{"sandboxes:execute"}, 204},
		{[]string{"sandboxes:read", "sandboxes:write", "sandboxes:execute", "templates:read", "templates:write"}, 204},
	} {
		id := strings.ReplaceAll(uuid.NewString(), "-", "")
		seed := sha256.Sum256([]byte("fixture-secret-" + id))
		secret := base64.RawURLEncoding.EncodeToString(seed[:])
		hash := sha256.Sum256([]byte(secret))
		_, err := store.CreateAPIKey(t.Context(), tenant, auth.APIKey{ID: id, Name: "command scope", Prefix: "iso_" + id + "_", Scopes: tc.scopes}, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		identity, err := store.AuthenticateAPIKey(t.Context(), id, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(identity.Scopes, ",") != strings.Join(tc.scopes, ",") {
			t.Fatalf("stored scopes changed: %v", identity.Scopes)
		}
		request := httptest.NewRequest("POST", "/v1/sandboxes/"+uuid.NewString()+"/commands/run", nil)
		request.Header.Set("X-API-Key", "iso_"+id+"_"+secret)
		response := httptest.NewRecorder()
		auth.Middleware(&auth.Access{Keys: store}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("scopes=%v status=%d want=%d", tc.scopes, response.Code, tc.want)
		}
	}
}
