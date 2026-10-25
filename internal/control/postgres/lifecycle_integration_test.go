package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/control/operation"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
)

func TestPostgresSandboxLifecycleOperations(t *testing.T) {
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
	tenantID, err := store.ResolveTenant(ctx, "https://lifecycle-"+time.Now().Format("150405.000000000")+".example.test", "owner", "Lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM sandbox_operations WHERE tenant_id = $1::uuid`, tenantID)
		cleanupTestTenants(t, databaseURL, tenantID)
	}()
	item, err := store.CreateSandbox(ctx, tenantID, "lifecycle", json.RawMessage(`{}`), "t3-base")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE sandbox_operations SET status = 'succeeded' WHERE sandbox_id = $1::uuid AND action = 'create'`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE sandboxes SET node_id = 'lifecycle-node', desired_state = 'stopped', observed_state = 'stopped' WHERE id = $1::uuid`, item.ID); err != nil {
		t.Fatal(err)
	}

	started, err := store.StartSandbox(ctx, tenantID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.DesiredState != "running" || started.ObservedState != "requested" {
		t.Fatalf("start = %+v", started)
	}
	var payload []byte
	if err := store.pool.QueryRow(ctx, `SELECT payload FROM sandbox_operations WHERE sandbox_id = $1::uuid AND action = 'start'`, item.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var lifecycle operation.LifecyclePayload
	if err := json.Unmarshal(payload, &lifecycle); err != nil || lifecycle.Revision != 2 {
		t.Fatalf("start payload = %s, %v", payload, err)
	}
	if _, err := store.StartSandbox(ctx, tenantID, item.ID); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatalf("duplicate start = %v, want conflict", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE sandbox_operations SET status = 'succeeded' WHERE sandbox_id = $1::uuid AND action = 'start'`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE sandboxes SET desired_state = 'running', observed_state = 'running' WHERE id = $1::uuid`, item.ID); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.StopSandbox(ctx, tenantID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.DesiredState != "stopped" || stopped.ObservedState != "requested" {
		t.Fatalf("stop = %+v", stopped)
	}
	if err := store.pool.QueryRow(ctx, `SELECT payload FROM sandbox_operations WHERE sandbox_id = $1::uuid AND action = 'stop'`, item.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &lifecycle); err != nil || lifecycle.Revision != 3 {
		t.Fatalf("stop payload = %s, %v", payload, err)
	}
}
