package skillmarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixtureAuth struct{ tenant, credential string }

func (a fixtureAuth) Authenticate(context.Context, string) (auth.Identity, error) {
	return auth.Identity{TenantID: a.tenant, Credential: a.credential, Scopes: []string{"sandboxes:read", "sandboxes:write"}}, nil
}

type fixtureRuntime struct {
	request *nodev1.SyncSkillsRequest
	fail    bool
}

func (f *fixtureRuntime) NodeID() string { return "skill-fixture" }
func (f *fixtureRuntime) SyncSkills(_ context.Context, r *nodev1.SyncSkillsRequest) (*nodev1.SyncSkillsResponse, error) {
	f.request = r
	if f.fail {
		return nil, fmt.Errorf("offline fixture")
	}
	return &nodev1.SyncSkillsResponse{RestartRequired: true}, nil
}
func TestPostgresSkillAccountIsolationAndSync(t *testing.T) {
	database := os.Getenv("ISODO_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires disposable PostgreSQL; scripts/test-mcp-market-postgres.sh")
	}
	pool, err := pgxpool.New(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenant, other, sandbox := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{tenant, other} {
		if _, err = pool.Exec(t.Context(), `INSERT INTO tenants(id) VALUES($1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM sandboxes WHERE tenant_id=ANY($1::uuid[])`, []string{tenant, other})
		pool.Exec(ctx, `DELETE FROM skill_favorites WHERE tenant_id=ANY($1::uuid[])`, []string{tenant, other})
		pool.Exec(ctx, `DELETE FROM skill_catalog WHERE tenant_id=ANY($1::uuid[])`, []string{tenant, other})
		pool.Exec(ctx, `DELETE FROM tenants WHERE id=ANY($1::uuid[])`, []string{tenant, other})
	}()
	_, err = pool.Exec(t.Context(), `INSERT INTO sandboxes(id,tenant_id,name,desired_state,observed_state,ownership_generation,revision,node_id) VALUES($1,$2,'skill-test','running','running',1,1,'skill-fixture')`, sandbox, tenant)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("隔离数据库中的 Skill 制作、收藏与同步验证开始；Sandbox ID: %s（Runtime 为 fixture）", sandbox)
	runtime := &fixtureRuntime{}
	h := New(pool, runtime)
	call := func(account, credential, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer fixture")
		rec := httptest.NewRecorder()
		auth.Middleware(fixtureAuth{account, credential}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(path, "sandboxes") {
				h.Sandbox(w, r, sandbox)
			} else if strings.Contains(path, "favorite") {
				h.Favorite(w, r, strings.Split(path, "/")[3])
			} else if method == "POST" {
				h.Create(w, r)
			} else {
				h.Catalog(w, r)
			}
		})).ServeHTTP(rec, req)
		return rec
	}
	created := call(tenant, "session", "POST", "/v1/skills", `{"name":"private-review","description":"Read only fixture instructions","body":"Return SKILL_FIXTURE_OK"}`)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var entry Entry
	json.Unmarshal(created.Body.Bytes(), &entry)
	if response := call(tenant, "api_key", "GET", "/v1/skills", ""); response.Code != 403 || !strings.Contains(response.Body.String(), "SESSION_REQUIRED") {
		t.Fatal("API key accepted")
	}
	if strings.Contains(call(other, "session", "GET", "/v1/skills", "").Body.String(), "private-review") {
		t.Fatal("private skill leaked")
	}
	if call(other, "session", "PUT", "/v1/skills/"+entry.ID+"/favorite", "").Code != 404 {
		t.Fatal("foreign skill favorited")
	}
	selection := `{"skill_ids":["` + entry.ID + `"]}`
	if call(other, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", selection).Code != 404 {
		t.Fatal("foreign sandbox allowed")
	}
	synced := call(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", selection)
	if synced.Code != 200 || runtime.request == nil || len(runtime.request.Skills) != 1 || runtime.request.Target.Revision != 1 {
		t.Fatal("sync failed", synced.Code, synced.Body.String())
	}
	if runtime.request.Skills[0].Body != "Return SKILL_FIXTURE_OK" {
		t.Fatal("wrong content")
	}
	// Unfavorite preserves a bound Skill until explicit sandbox deselection.
	if call(tenant, "session", "DELETE", "/v1/skills/"+entry.ID+"/favorite", "").Code != 204 {
		t.Fatal("unfavorite failed")
	}
	if call(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", selection).Code != 200 {
		t.Fatal("retained binding rejected")
	}
	removed := call(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", `{"skill_ids":[]}`)
	if removed.Code != 200 || len(runtime.request.Skills) != 0 {
		t.Fatal("remove failed")
	}
	if call(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", selection).Code != 409 {
		t.Fatal("unfavorited addition accepted")
	}
	call(tenant, "session", "PUT", "/v1/skills/"+entry.ID+"/favorite", "")
	runtime.fail = true
	pending := call(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", selection)
	if pending.Code != 202 || !strings.Contains(pending.Body.String(), `"sync_status":"failed"`) {
		t.Fatal("failed sync reported applied", pending.Body.String())
	}
	_, err = pool.Exec(t.Context(), `UPDATE sandboxes SET desired_state='stopped',observed_state='stopped',revision=2 WHERE id=$1`, sandbox)
	if err != nil {
		t.Fatal(err)
	}
	runtime.request = nil
	pending = call(tenant, "session", "PUT", "/v1/sandboxes/"+sandbox+"/skills", selection)
	if pending.Code != 202 || runtime.request != nil {
		t.Fatal("stopped sandbox forwarded")
	}
	_, err = pool.Exec(t.Context(), `UPDATE sandboxes SET deletion_requested_at=now() WHERE id=$1`, sandbox)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM sandbox_skills WHERE sandbox_id=$1`, sandbox).Scan(&count)
	if count != 0 {
		t.Fatal("deletion left selection")
	}
	t.Logf("账号隔离、显式收藏与选择、失败/停止准入和删除清理通过；Sandbox ID: %s；不是 CHV E2E", sandbox)
	if call(tenant, "session", "GET", "/v1/sandboxes/"+sandbox+"/skills", "").Code != 404 {
		t.Fatal("deleting sandbox accessible")
	}
}
