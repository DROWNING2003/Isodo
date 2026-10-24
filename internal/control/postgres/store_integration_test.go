package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresSandboxTenantIsolation(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set ISODO_TEST_DATABASE_URL to a database with migrations applied")
	}
	ctx := t.Context()
	store, err := New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	issuer := "https://integration-" + suffix + ".example.test"
	const subjectA = "owner-a"
	const subjectB = "owner-b"
	tenantA, err := store.ResolveTenant(ctx, issuer, subjectA, "A")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := store.ResolveTenant(ctx, issuer, subjectB, "B")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTestTenants(t, databaseURL, tenantA, tenantB)

	concurrentIssuer := issuer + "/concurrent"
	const concurrentSubject = "same-subject"
	const concurrentRequests = 8
	var wait sync.WaitGroup
	results := make([]string, concurrentRequests)
	errorsByRequest := make([]error, concurrentRequests)
	for i := range concurrentRequests {
		wait.Go(func() {
			results[i], errorsByRequest[i] = store.ResolveTenant(ctx, concurrentIssuer, concurrentSubject, "parallel")
		})
	}
	wait.Wait()
	for i, err := range errorsByRequest {
		if err != nil {
			t.Fatalf("concurrent identity request %d: %v", i, err)
		}
		if results[i] != results[0] {
			t.Fatalf("concurrent identity requests created multiple tenants: %v", results)
		}
	}
	defer cleanupTestTenantByIdentity(t, databaseURL, concurrentIssuer, concurrentSubject)

	if _, err := store.RegisterSystemTemplate(ctx, "linux-base", "test fixture", strings.Repeat("a", 64), "test-bucket", ""); err != nil {
		t.Fatal(err)
	}
	owned, err := store.CreateSandbox(ctx, tenantA, "owner-a", json.RawMessage(`{"visibility":"private"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateSandbox(ctx, tenantB, "owner-b", json.RawMessage(`{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	listed, total, err := store.ListSandboxes(ctx, tenantA, 50, 0)
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != owned.ID {
		t.Fatalf("owner list = (%v, %d, %v), want only %s", listed, total, err, owned.ID)
	}
	if _, err := store.GetSandbox(ctx, tenantA, foreign.ID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("foreign Get error = %v, want ErrNotFound", err)
	}
	name := "stolen"
	if _, err := store.UpdateSandbox(ctx, tenantA, foreign.ID, sandbox.Patch{Name: &name}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("foreign Update error = %v, want ErrNotFound", err)
	}
	if err := store.DeleteSandbox(ctx, tenantA, foreign.ID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("foreign Delete error = %v, want ErrNotFound", err)
	}
	updated, err := store.UpdateSandbox(ctx, tenantA, owned.ID, sandbox.Patch{Name: &name})
	if err != nil || updated.Name != name {
		t.Fatalf("owner Update = (%v, %v), want updated name", updated, err)
	}
	if err := store.DeleteSandbox(ctx, tenantA, owned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSandbox(ctx, tenantA, owned.ID); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("Get after soft delete error = %v, want ErrNotFound", err)
	}
}

func cleanupTestTenants(t *testing.T, databaseURL string, tenantIDs ...string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Errorf("open cleanup connection: %v", err)
		return
	}
	defer pool.Close()
	for _, tenantID := range tenantIDs {
		_, _ = pool.Exec(ctx, `DELETE FROM sandboxes WHERE tenant_id = $1::uuid`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM user_identities WHERE tenant_id = $1::uuid`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1::uuid`, tenantID)
	}
}

func cleanupTestTenantByIdentity(t *testing.T, databaseURL, issuer, subject string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Errorf("open cleanup connection: %v", err)
		return
	}
	defer pool.Close()
	var tenantID string
	if err := pool.QueryRow(ctx, `SELECT tenant_id::text FROM user_identities WHERE issuer = $1 AND subject = $2`, issuer, subject).Scan(&tenantID); err != nil {
		t.Errorf("find concurrent test tenant: %v", err)
		return
	}
	_, _ = pool.Exec(ctx, `DELETE FROM user_identities WHERE tenant_id = $1::uuid`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1::uuid`, tenantID)
}
