package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"strings"
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
	reference := "lifecycle-" + uuid.NewString()
	if _, err := store.RegisterSystemTemplate(ctx, reference, "fixture", strings.Repeat("a", 64), "test-bucket", ""); err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateSandbox(ctx, tenantID, "lifecycle", json.RawMessage(`{}`), reference)
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
	if started.Revision != "2" || started.LastAction == nil || started.LastAction.Revision != "2" || started.LastAction.Type != "start" || started.LastAction.Status != "accepted" {
		t.Fatalf("start action=%+v", started)
	}
	read, err := store.GetSandbox(ctx, tenantID, item.ID)
	if err != nil || read.Revision != "2" || read.LastAction == nil || read.LastAction.Status != "accepted" {
		t.Fatalf("get=%+v err=%v", read, err)
	}
	renamed := "renamed"
	read, err = store.UpdateSandbox(ctx, tenantID, item.ID, sandbox.Patch{Name: &renamed})
	if err != nil || read.Name != renamed || read.Revision != "2" || read.LastAction == nil || read.LastAction.Revision != "2" {
		t.Fatalf("patch=%+v err=%v", read, err)
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
	if stopped.Revision != "3" || stopped.LastAction == nil || stopped.LastAction.Type != "stop" {
		t.Fatalf("stop action=%+v", stopped)
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
	var stopOperationID string
	if err := store.pool.QueryRow(ctx, `UPDATE sandbox_operations SET status='running',lease_owner='failed-stop' WHERE sandbox_id=$1::uuid AND action='stop' RETURNING id::text`, item.ID).Scan(&stopOperationID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSandboxLifecycle(ctx, operation.LifecycleJob{OperationID: stopOperationID, WorkerID: "failed-stop", SandboxID: item.ID, TenantID: tenantID, Action: "stop", Revision: 3, OwnershipGeneration: 1}, "STOP_FAILED"); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetSandbox(ctx, tenantID, item.ID)
	if err != nil || failed.DesiredState != "stopped" || failed.LastAction == nil || failed.LastAction.Status != "failed" || failed.LastAction.Error == nil || failed.LastAction.Error.Code != "STOP_FAILED" {
		t.Fatalf("failed stop=%+v err=%v", failed, err)
	}
	retry, err := store.StopSandbox(ctx, tenantID, item.ID)
	if err != nil || retry.Revision != "4" {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
}

func TestLateCompletionCannotOverwriteCurrentSandbox(t *testing.T) {
	databaseURL := os.Getenv("ISODO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	store, err := New(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, action := range []string{"create", "start"} {
		for _, fence := range []string{"revision", "generation", "deletion"} {
			t.Run(action+"/"+fence, func(t *testing.T) {
				ctx := t.Context()
				tenant, err := store.ResolveTenant(ctx, "https://fence-"+uuid.NewString()+".test", "owner", "Owner")
				if err != nil {
					t.Fatal(err)
				}
				item, err := store.CreateSandbox(ctx, tenant, "fenced", json.RawMessage(`{}`), sandbox.LocalLimaTemplateReference)
				if err != nil {
					t.Fatal(err)
				}
				revision := uint64(1)
				if action == "start" {
					revision = 2
					if _, err := store.pool.Exec(ctx, `INSERT INTO sandbox_operations(tenant_id,sandbox_id,action,request_hash,payload) VALUES($1::uuid,$2::uuid,'start',$3,'{"revision":2,"ownership_generation":1}')`, tenant, item.ID, strings.Repeat("a", 64)); err != nil {
						t.Fatal(err)
					}
				}
				var operationID string
				if err := store.pool.QueryRow(ctx, `UPDATE sandbox_operations SET status='running',lease_owner='fence-worker' WHERE sandbox_id=$1::uuid AND action=$2 RETURNING id::text`, item.ID, action).Scan(&operationID); err != nil {
					t.Fatal(err)
				}
				nextRevision, generation := revision, uint64(1)
				var deleted any
				switch fence {
				case "revision":
					nextRevision++
				case "generation":
					generation++
				case "deletion":
					deleted = time.Now().UTC()
				}
				if _, err := store.pool.Exec(ctx, `UPDATE sandboxes SET desired_state='stopped',observed_state='stopped',revision=$2,ownership_generation=$3,deleted_at=$4 WHERE id=$1::uuid`, item.ID, nextRevision, generation, deleted); err != nil {
					t.Fatal(err)
				}
				if action == "create" {
					err = store.CompleteSandboxCreate(ctx, operation.CreateJob{OperationID: operationID, WorkerID: "fence-worker", SandboxID: item.ID, TenantID: tenant, Revision: revision, OwnershipGeneration: 1}, "")
				} else {
					err = store.CompleteSandboxLifecycle(ctx, operation.LifecycleJob{OperationID: operationID, WorkerID: "fence-worker", SandboxID: item.ID, TenantID: tenant, Action: action, Revision: revision, OwnershipGeneration: 1}, "")
				}
				if err != nil {
					t.Fatal(err)
				}
				var desired, observed string
				if err := store.pool.QueryRow(ctx, `SELECT desired_state,observed_state FROM sandboxes WHERE id=$1::uuid`, item.ID).Scan(&desired, &observed); err != nil {
					t.Fatal(err)
				}
				if desired != "stopped" || observed != "stopped" {
					t.Fatalf("late completion overwrote current state: %s/%s", desired, observed)
				}
			})
		}
	}
}
