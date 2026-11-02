package mcpmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/injection"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresDCRProviders(t *testing.T) {
	database := os.Getenv("ISODO_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	pool, err := pgxpool.New(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, provider := range []string{"notion"} {
		t.Run(provider, func(t *testing.T) {
			tenant, sandboxID := uuid.NewString(), uuid.NewString()
			if _, err := pool.Exec(t.Context(), `INSERT INTO tenants(id) VALUES($1)`, tenant); err != nil {
				t.Fatal(err)
			}
			_, err := pool.Exec(t.Context(), `INSERT INTO sandboxes(id,tenant_id,name,desired_state,observed_state,ownership_generation,revision,node_id) VALUES($1,$2,'dcr-fixture','running','running',1,1,'mcp-test-node')`, sandboxID, tenant)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				for _, table := range []string{"mcp_sandbox_bindings", "mcp_oauth_transactions", "mcp_oauth_connections", "mcp_favorites", "sandboxes", "tenants"} {
					column := "tenant_id"
					if table == "tenants" {
						column = "id"
					}
					_, _ = pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE "+column+"=$1", tenant)
				}
			}()
			sealer, _ := injection.NewSealer(bytes.Repeat([]byte{7}, 32))
			runtime := &fixtureRuntime{}
			h := New(pool, sealer, Config{ClientID: "github-only", ClientSecret: "github-secret", RedirectURI: "https://api.drowning.top:18443/v1/mcp/oauth/callback"}, runtime)
			fixture := &dcrFixture{provider: provider}
			h.http = fixture.client()
			app := h.Wrap(http.NotFoundHandler(), fixedAuth{tenant: tenant})
			request := func(method, path, body string, want int) []byte {
				t.Helper()
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer fixture")
				app.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body)
				}
				return w.Body.Bytes()
			}
			catalog := request("GET", "/v1/mcp/catalog", "", 200)
			if bytes.Contains(catalog, []byte(`"market_mcp_id":"supabase"`)) {
				t.Fatal("removed provider exposed in catalog")
			}
			request("POST", "/v1/mcp/connections/supabase/authorize", "", 404)
			request("PUT", "/v1/mcp/favorites/supabase", "", 404)
			start := func() url.Values {
				t.Helper()
				body := ""

				raw := request("POST", "/v1/mcp/connections/"+provider+"/authorize", body, 200)
				var response struct {
					URL string `json:"authorization_url"`
				}
				_ = json.Unmarshal(raw, &response)
				u, err := url.Parse(response.URL)
				if err != nil {
					t.Fatal(err)
				}
				v := u.Query()
				if v.Get("code_challenge_method") != "S256" || len(v.Get("state")) != 43 || v.Get("redirect_uri") != h.config.RedirectURI {
					t.Fatal("PKCE/state/redirect mismatch")
				}
				return v
			}
			callback := func(values url.Values, issuer string, want int) {
				t.Helper()
				r := httptest.NewRequest("GET", h.config.RedirectURI+"?"+url.Values{"state": {values.Get("state")}, "code": {"fixture-code"}, "iss": {issuer}}.Encode(), nil)
				w := httptest.NewRecorder()
				h.Callback(w, r)
				if w.Code != want {
					t.Fatalf("callback %d want=%d body=%s", w.Code, want, w.Body)
				}
			}
			request("PUT", "/v1/mcp/favorites/"+provider, "", 204)

			as := "https://mcp.notion.com"
			safe, unsafe := "notion-search", "notion-create-pages"

			values := start()
			callback(values, as, 200)
			callback(values, as, 400)
			if fixture.registrations.Load() != 1 {
				t.Fatal("initial DCR registration missing")
			}
			values = start()
			if fixture.registrations.Load() != 1 {
				t.Fatal("registration not reused")
			}
			callback(values, "https://evil.test", 400)
			values = start()
			callback(values, "", 400)
			request("GET", "/v1/mcp/catalog", "", 200)
			raw := request("GET", "/v1/mcp/connections/"+provider+"/tools", "", 200)
			if bytes.Contains(raw, []byte(unsafe)) || !bytes.Contains(raw, []byte(safe)) || bytes.Contains(raw, []byte("fixture-access")) {
				t.Fatal("tool filtering or token redaction failed")
			}
			request("PUT", "/v1/sandboxes/"+sandboxID+"/mcp/"+provider, "", 200)
			if len(runtime.request.Bindings) != 1 || runtime.request.Bindings[0].MarketMcpId != provider {
				t.Fatal("Node binding not synced")
			}
			binding := runtime.request.Bindings[0]
			broker := func(tool, args string, want int) {
				t.Helper()
				r := httptest.NewRequest("POST", "/internal/mcp/"+binding.BindingId, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+args+`}}`))
				r.SetPathValue("bindingID", binding.BindingId)
				r.Header.Set("Authorization", "Bearer "+binding.Capability)
				r.Header.Set("X-Isodo-Sandbox", sandboxID)
				r.Header.Set("X-Isodo-Node", "mcp-test-node")
				r.Header.Set("X-Isodo-Generation", "1")
				r.Header.Set("X-Isodo-Revision", "1")
				w := httptest.NewRecorder()
				h.Broker(w, r)
				if w.Code != want {
					t.Fatalf("broker %s status=%d body=%s", tool, w.Code, w.Body)
				}
			}
			broker(safe, `{}`, 200)
			broker(unsafe, `{}`, 403)

			// Force expiration in durable encrypted state, then contend across handlers.
			var nonce, sealed []byte
			if err := pool.QueryRow(t.Context(), `SELECT nonce,ciphertext FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, provider).Scan(&nonce, &sealed); err != nil {
				t.Fatal(err)
			}
			data, err := sealer.Open(nonce, sealed)
			if err != nil {
				t.Fatal(err)
			}
			var token tokenMaterial
			_ = json.Unmarshal(data, &token)
			token.ExpiresAt = time.Now().Add(-time.Minute)
			token.Refresh = "old-refresh"
			data, _ = json.Marshal(token)
			nonce, sealed, _ = sealer.Seal(data)
			_, err = pool.Exec(t.Context(), `UPDATE mcp_oauth_connections SET nonce=$3,ciphertext=$4,expires_at=$5 WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, provider, nonce, sealed, token.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			other := New(pool, sealer, h.config, nil)
			other.http = fixture.client()
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for i := range 8 {
				wg.Go(func() {
					selected := h
					if i%2 == 0 {
						selected = other
					}
					_, err := selected.freshToken(t.Context(), tenant, provider)
					results <- err
				})
			}
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatal(err)
				}
			}
			if fixture.refreshes.Load() != 1 {
				t.Fatalf("rotation refreshed %d times", fixture.refreshes.Load())
			}
			current, err := h.freshToken(t.Context(), tenant, provider)
			if err != nil || current.Refresh != "fixture-rotated" {
				t.Fatal("rotated token not persisted")
			}
			if bytes.Contains(sealed, []byte("old-refresh")) {
				t.Fatal("refresh token not encrypted")
			}
			// Disconnect cancels pending callbacks and future binding requests immediately.
			values = start()
			request("DELETE", "/v1/mcp/connections/"+provider, "", 204)
			callback(values, as, 400)
			broker(safe, `{}`, 403)
			if _, err := h.freshToken(t.Context(), tenant, provider); err == nil {
				t.Fatal("disconnected token usable")
			}
			// Registry expiration triggers new registration. Failure never falls back to PAT.
			var regNonce, regSealed []byte
			err = pool.QueryRow(t.Context(), `SELECT nonce,ciphertext FROM mcp_oauth_registrations WHERE provider=$1`, provider).Scan(&regNonce, &regSealed)
			if err != nil {
				t.Fatal(err)
			}
			data, _ = sealer.Open(regNonce, regSealed)
			var registration grantContext
			_ = json.Unmarshal(data, &registration)
			registration.Client.SecretExpires = time.Now().Add(-time.Hour).Unix()
			data, _ = json.Marshal(registration)
			regNonce, regSealed, _ = sealer.Seal(data)
			_, err = pool.Exec(t.Context(), `UPDATE mcp_oauth_registrations SET nonce=$2,ciphertext=$3 WHERE provider=$1`, provider, regNonce, regSealed)
			if err != nil {
				t.Fatal(err)
			}
			values = start()
			callback(values, as, 200)
			if fixture.registrations.Load() != 2 {
				t.Fatal("expired registration reused")
			}
			// invalid_grant expires the connection, without restoring a disconnected row.
			current, _ = h.freshToken(t.Context(), tenant, provider)
			current.ExpiresAt = time.Now().Add(-time.Minute)
			data, _ = json.Marshal(current)
			nonce, sealed, _ = sealer.Seal(data)
			_, _ = pool.Exec(t.Context(), `UPDATE mcp_oauth_connections SET nonce=$3,ciphertext=$4 WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, provider, nonce, sealed)
			fixture.bad = "invalid_grant"
			if _, err := h.freshToken(t.Context(), tenant, provider); err == nil {
				t.Fatal("invalid_grant accepted")
			}
			var status string
			_ = pool.QueryRow(t.Context(), `SELECT status FROM mcp_oauth_connections WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, provider).Scan(&status)
			if status != "expired" {
				t.Fatal("invalid_grant did not expire grant")
			}
			fixture.bad = "invalid_client"
			data, _ = json.Marshal(current)
			nonce, sealed, _ = sealer.Seal(data)
			_, _ = pool.Exec(t.Context(), `UPDATE mcp_oauth_connections SET status='connected',nonce=$3,ciphertext=$4 WHERE tenant_id=$1 AND market_mcp_id=$2`, tenant, provider, nonce, sealed)
			if _, err := h.freshToken(t.Context(), tenant, provider); err == nil {
				t.Fatal("invalid client refresh accepted")
			}

			var registrationStatus string
			if err := pool.QueryRow(t.Context(), `SELECT status FROM mcp_oauth_registrations WHERE provider=$1`, provider).Scan(&registrationStatus); err != nil || registrationStatus != "invalid" {
				t.Fatal("invalid client did not invalidate registration")
			}
			fixture.bad = "register"
			body := ""

			request("POST", "/v1/mcp/connections/"+provider+"/authorize", body, 502)
			t.Logf("Provider=%s Sandbox ID=%s DCR/PKCE/issuer/复用/过期/刷新轮换/只读/撤销 PASS（fixture 上游与 Node，真实 PostgreSQL）", provider, sandboxID)

		})
	}
}
