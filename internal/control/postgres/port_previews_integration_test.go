package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/google/uuid"
)

func TestPortPreviewPersistenceAndDeletion(t *testing.T) {
	dsn := os.Getenv("ISODO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("需要专用且已迁移的 PostgreSQL")
	}
	ctx := t.Context()
	store, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tenant, err := store.ResolveTenant(ctx, "https://preview-"+uuid.NewString()+".test", "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTestTenants(t, dsn, tenant)
	item, err := store.CreateSandbox(ctx, tenant, "preview-policy", json.RawMessage(`{}`), sandbox.LocalLimaTemplateReference)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("[开始] 默认公开、持久化、生命周期清理 Sandbox ID=%s", item.ID)
	policy, err := store.GetPortPreview(ctx, tenant, item.ID, 3000)
	if err != nil || policy.Visibility != "public" || policy.Revision != 0 {
		t.Fatalf("default=%+v %v", policy, err)
	}
	private, err := store.SetPortPreview(ctx, tenant, item.ID, 3000, "private")
	if err != nil || private.Revision != 1 {
		t.Fatalf("private=%+v %v", private, err)
	}
	restarted, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for _, state := range []string{"stopped", "running"} {
		_, err := store.pool.Exec(ctx, `UPDATE sandboxes SET desired_state=$2,observed_state=$2 WHERE id=$1::uuid`, item.ID, state)
		if err != nil {
			t.Fatal(err)
		}
		p, err := restarted.GetPortPreview(ctx, tenant, item.ID, 3000)
		if err != nil || p != private {
			t.Fatalf("state=%s policy=%+v err=%v", state, p, err)
		}
	}
	if _, err := store.SetPortPreview(ctx, uuid.NewString(), item.ID, 3000, "public"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("cross tenant=%v", err)
	}
	public, err := store.SetPortPreview(ctx, tenant, item.ID, 3000, "public")
	if err != nil || public.Revision != 2 {
		t.Fatalf("public=%+v %v", public, err)
	}
	for _, port := range []uint16{0, 1024, 1080, 8080, 31024, 31080, 38080} {
		if _, err := store.SetPortPreview(ctx, tenant, item.ID, port, "public"); err == nil {
			t.Fatalf("reserved=%d admitted", port)
		}
	}
	// Race a policy write with delete intent. Both transactions acquire the same
	// Sandbox row lock, so a post-delete upsert must never resurrect a policy.
	done := make(chan error, 1)
	go func() {
		_, err := restarted.SetPortPreview(context.WithoutCancel(ctx), tenant, item.ID, 3000, "private")
		done <- err
	}()
	if _, err := store.DeleteSandbox(ctx, tenant, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatal(err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM sandbox_port_previews WHERE sandbox_id=$1::uuid`, item.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("delete left %d policies: %v", count, err)
	}
	if _, err := store.SetPortPreview(ctx, tenant, item.ID, 3000, "public"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("resurrected=%v", err)
	}
	t.Logf("[成功] 策略持久化、Stop/Start 状态保留、删除与并发清理 Sandbox ID=%s", item.ID)
}
