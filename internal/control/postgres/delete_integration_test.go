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

func TestSandboxDeleteWaitsForNodeCleanup(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	store, err := New(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	tenant, err := store.ResolveTenant(ctx, "https://delete-"+uuid.NewString()+".test", "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM sandbox_operations WHERE tenant_id=$1::uuid`, tenant)
		cleanupTestTenants(t, databaseURL, tenant)
	}()
	item, err := store.CreateSandbox(ctx, tenant, "cleanup", json.RawMessage(`{}`), sandbox.LocalLimaTemplateReference)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := store.DeleteSandbox(ctx, tenant, item.ID)
	if err != nil || accepted.Revision != "2" || accepted.Deletion == nil || accepted.Deletion.CompletedAt != nil {
		t.Fatalf("accepted=%+v err=%v", accepted, err)
	}
	repeated, err := store.DeleteSandbox(ctx, tenant, item.ID)
	if err != nil || repeated.Revision != accepted.Revision {
		t.Fatalf("repeated=%+v err=%v", repeated, err)
	}
	if _, err := store.StartSandbox(ctx, tenant, item.ID); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatalf("start during deletion=%v", err)
	}
	list, total, err := store.ListSandboxes(ctx, tenant, 100, 0)
	if err != nil || total != 0 || len(list) != 0 {
		t.Fatalf("list=%+v total=%d err=%v", list, total, err)
	}
	job, err := store.ClaimSandboxLifecycle(ctx, "delete-worker", "delete-node")
	if err != nil || job.Action != "delete" || job.Revision != 2 || job.SandboxID != item.ID {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if err := store.CompleteSandboxLifecycle(ctx, job, "DELETE_FAILED"); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetSandbox(ctx, tenant, item.ID)
	if err != nil || failed.Deletion.CompletedAt != nil || failed.LastAction.Status != "failed" || failed.LastAction.Error.Code != "DELETE_FAILED" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	retry, err := store.DeleteSandbox(ctx, tenant, item.ID)
	if err != nil || retry.Revision != "3" {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	job, err = store.ClaimSandboxLifecycle(ctx, "delete-worker", "delete-node")
	if err != nil || job.Revision != 3 {
		t.Fatalf("retry job=%+v err=%v", job, err)
	}
	if err := store.CompleteSandboxLifecycle(ctx, job, ""); err != nil {
		t.Fatal(err)
	}
	completed, err := store.GetSandbox(ctx, tenant, item.ID)
	if err != nil || completed.Deletion.CompletedAt == nil || completed.LastAction.Status != "succeeded" || completed.ObservedState != "stopped" {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	repeated, err = store.DeleteSandbox(ctx, tenant, item.ID)
	if err != nil || repeated.Revision != "3" || repeated.Deletion.CompletedAt == nil {
		t.Fatalf("completed repeat=%+v err=%v", repeated, err)
	}
}

func TestLegacyLogicalDeletionStillRequestsCleanup(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	store, err := New(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	tenant, err := store.ResolveTenant(ctx, "https://legacy-delete-"+uuid.NewString()+".test", "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM sandbox_operations WHERE tenant_id=$1::uuid`, tenant)
		cleanupTestTenants(t, databaseURL, tenant)
	}()
	item, err := store.CreateSandbox(ctx, tenant, "legacy", json.RawMessage(`{}`), sandbox.LocalLimaTemplateReference)
	if err != nil {
		t.Fatal(err)
	}
	// Migration retains logical deletion intent without asserting runtime cleanup.
	if _, err := store.pool.Exec(ctx, `UPDATE sandboxes SET deletion_requested_at=now() WHERE id=$1::uuid`, item.ID); err != nil {
		t.Fatal(err)
	}
	accepted, err := store.DeleteSandbox(ctx, tenant, item.ID)
	if err != nil || accepted.Revision != "2" || accepted.LastAction == nil || accepted.LastAction.Type != "delete" || accepted.Deletion.CompletedAt != nil {
		t.Fatalf("legacy cleanup=%+v err=%v", accepted, err)
	}
}
