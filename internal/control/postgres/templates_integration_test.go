package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

func TestTemplateRegistryIsolationAndBuilds(t *testing.T) {
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
	issuer := fmt.Sprintf("https://templates-%d.test", time.Now().UnixNano())
	a, err := store.ResolveTenant(ctx, issuer, "a", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.ResolveTenant(ctx, issuer, "b", "B")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		// Remove references in dependency order only for this test's tenants.
		_, err := store.pool.Exec(cleanup, `DELETE FROM template_tags WHERE template_id IN (SELECT id FROM templates WHERE owner_tenant_id IN ($1::uuid,$2::uuid));
  `, a, b)
		if err != nil {
			t.Error(err)
		}
		_, err = store.pool.Exec(cleanup, `DELETE FROM template_builds WHERE template_id IN (SELECT id FROM templates WHERE owner_tenant_id IN ($1::uuid,$2::uuid))`, a, b)
		if err != nil {
			t.Error(err)
		}
		_, err = store.pool.Exec(cleanup, `DELETE FROM templates WHERE owner_tenant_id IN ($1::uuid,$2::uuid)`, a, b)
		if err != nil {
			t.Error(err)
		}
		cleanupTestTenants(t, databaseURL, a, b)
	})
	handler := httpapi.NewHandler(nil, auth.NewAPIKeyHandler(store), sandbox.NewHandler(store), &auth.Access{Keys: store, Bearer: testBearer{"Bearer a": {TenantID: a}, "Bearer b": {TenantID: b}}}, template.NewHandler(store))
	request := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if strings.HasPrefix(user, "iso_") {
			req.Header.Set("X-API-Key", user)
		} else if user != "" {
			req.Header.Set("Authorization", "Bearer "+user)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			actor := user
			if strings.HasPrefix(actor, "iso_") {
				actor = "api_key"
			}
			t.Fatalf("%s %s (%s): got %d want %d: %s", method, path, actor, w.Code, want, w.Body.String())
		}
		return w
	}

	t.Run("system templates are read only", func(t *testing.T) {
		var systemID, buildID string
		if err := store.pool.QueryRow(ctx, `INSERT INTO templates(scope,visibility,name) VALUES('system','public','linux-base-test') RETURNING id::text`).Scan(&systemID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanup := context.Background()
			_, _ = store.pool.Exec(cleanup, `DELETE FROM system_template_catalog WHERE template_id=$1::uuid`, systemID)
			_, _ = store.pool.Exec(cleanup, `DELETE FROM template_builds WHERE template_id=$1::uuid`, systemID)
			_, _ = store.pool.Exec(cleanup, `DELETE FROM templates WHERE id=$1::uuid`, systemID)
		})
		if err := store.pool.QueryRow(ctx, `INSERT INTO template_builds(template_id,status,manifest_digest,artifact_bucket,artifact_prefix) VALUES($1::uuid,'ready',repeat('b',64),'isodo-test-fixture','templates/sha256/system-fixture') RETURNING id::text`, systemID).Scan(&buildID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO system_template_catalog(name,template_id,build_id) VALUES($1,$2::uuid,$3::uuid)`, "linux-base-"+systemID, systemID, buildID); err != nil {
			t.Fatal(err)
		}
		list := request("a", "GET", "/v1/system-templates", "", 200)
		if !strings.Contains(list.Body.String(), systemID) {
			t.Fatal("system default absent from catalog")
		}
		path := "/v1/templates/" + systemID
		request("a", "GET", path, "", 200)
		request("a", "PATCH", path, `{"name":"changed"}`, 404)
		request("a", "DELETE", path, "", 404)
		request("a", "POST", path+"/builds", `{"source_build_id":"`+buildID+`"}`, 404)
	})

	request("", "GET", "/v1/templates", "", 401)
	request("a", "POST", "/v1/templates", `{"name":"bad","owner_tenant_id":"`+b+`"}`, 400)
	request("a", "POST", "/v1/templates", `{"name":"bad","visibility":"team"}`, 400)
	created := request("a", "POST", "/v1/templates", `{"name":"base-custom"}`, 201)
	var item api.Template
	if err = json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if !item.Owned || item.Visibility != "private" || item.Scope != "personal" {
		t.Fatalf("unexpected ownership/defaults: %+v", item)
	}
	path := "/v1/templates/" + item.ID

	keyResponse := request("a", "POST", "/v1/api-keys", `{"name":"template-read-test","scopes":["templates:read"]}`, 201)
	var key struct {
		ID     string `json:"id"`
		Secret string `json:"api_key"`
	}
	if err = json.Unmarshal(keyResponse.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	request(key.Secret, "GET", path, "", 200)
	request(key.Secret, "POST", "/v1/templates", `{"name":"denied"}`, 403)
	request(key.Secret, "DELETE", "/v1/api-keys/"+key.ID, "", 401)
	request("a", "DELETE", "/v1/api-keys/"+key.ID, "", 204)
	request(key.Secret, "GET", path, "", 401)
	foreign := request("b", "GET", path, "", 404)
	missing := request("b", "GET", "/v1/templates/00000000-0000-0000-0000-000000000000", "", 404)
	if foreign.Body.String() != missing.Body.String() {
		t.Fatal("private existence leaked")
	}
	request("b", "PATCH", path, `{"visibility":"public"}`, 404)
	request("b", "DELETE", path, "", 404)
	list := request("b", "GET", "/v1/templates?scope=available", "", 200)
	if strings.Contains(list.Body.String(), item.ID) {
		t.Fatal("private listed to another user")
	}
	request("a", "PATCH", path, `{"visibility":"public"}`, 200)
	request("b", "GET", path, "", 200)
	request("b", "PATCH", path, `{"name":"hijacked"}`, 404)
	request("b", "DELETE", path, "", 404)
	list = request("b", "GET", "/v1/templates?scope=public", "", 200)
	if !strings.Contains(list.Body.String(), item.ID) {
		t.Fatal("public template absent")
	}
	request("a", "GET", "/v1/templates?limit=0", "", 400)
	request("a", "GET", "/v1/templates?scope=team", "", 400)
	// Fixture simulates a previously verified artifact; this test exercises registry transactions, not VM validation.
	var source string
	err = store.pool.QueryRow(ctx, `INSERT INTO template_builds(template_id,status,manifest_digest,artifact_bucket,artifact_prefix) VALUES($1::uuid,'ready',repeat('a',64),'isodo-test-fixture','templates/sha256/personal-fixture') RETURNING id::text`, item.ID).Scan(&source)
	if err != nil {
		t.Fatal(err)
	}
	request("a", "PUT", path+"/tags/default", `{"build_id":"`+source+`","expected_build_id":""}`, 200)
	request("a", "PUT", path+"/tags/default", `{"build_id":"`+source+`","expected_build_id":""}`, 409)
	request("b", "PUT", path+"/tags/default", `{"build_id":"`+source+`"}`, 404)
	request("b", "GET", path+"/tags/default", "", 200)
	forkResponse := request("b", "POST", path+"/fork", `{"name":"my-fork","build_id":"`+source+`"}`, 201)
	var fork api.Template
	if err = json.Unmarshal(forkResponse.Body.Bytes(), &fork); err != nil {
		t.Fatal(err)
	}
	if !fork.Owned || fork.Visibility != "private" {
		t.Fatal("fork not privately owned by caller")
	}
	request("a", "GET", "/v1/templates/"+fork.ID, "", 404)
	queued := request("a", "POST", path+"/builds", `{"source_build_id":"`+source+`","commands":[["/bin/echo","ok"]]}`, 202)
	var build api.TemplateBuild
	if err = json.Unmarshal(queued.Body.Bytes(), &build); err != nil {
		t.Fatal(err)
	}
	if build.Status != "queued" || build.OperationID == "" {
		t.Fatalf("build not durably queued: %+v", build)
	}
	request("a", "GET", path+"/builds/"+build.ID, "", 200)
	request("b", "GET", path+"/builds/"+build.ID, "", 404)
	request("a", "PUT", path+"/tags/default", `{"build_id":"`+build.ID+`"}`, 404)
	request("a", "PATCH", path, `{"visibility":"private"}`, 200)
	request("b", "GET", path+"/tags/default", "", 404)
	request("b", "POST", path+"/fork", `{"name":"denied","build_id":"`+source+`"}`, 404)
	request("b", "GET", "/v1/templates/"+fork.ID+"/tags/default", "", 200)
	request("a", "DELETE", path, "", 204)
	request("a", "GET", path, "", 404)
	var status string
	if err = store.pool.QueryRow(ctx, `SELECT status FROM template_builds WHERE id=$1::uuid`, build.ID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("deleted template left runnable queued build: %s %v", status, err)
	}
}
