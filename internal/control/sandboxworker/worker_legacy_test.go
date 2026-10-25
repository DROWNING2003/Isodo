package sandboxworker

import (
	"context"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/control/operation"
)

type rejectingQueue struct{ code string }

func (q *rejectingQueue) ClaimSandboxCreate(context.Context, string, string) (operation.CreateJob, error) {
	return operation.CreateJob{}, operation.ErrNoCreateOperation
}
func (q *rejectingQueue) ExtendSandboxCreateLease(context.Context, string, string) error { return nil }
func (q *rejectingQueue) CompleteSandboxCreate(_ context.Context, _ operation.CreateJob, code string) error {
	q.code = code
	return nil
}
func (q *rejectingQueue) RetrySandboxCreate(context.Context, operation.CreateJob) error { return nil }

func TestRunCreateRejectsLegacyCHVJobWithoutResources(t *testing.T) {
	queue := &rejectingQueue{}
	worker := &Worker{queue: queue, backend: "chv"}

	err := worker.runCreate(t.Context(), operation.CreateJob{OperationID: "operation", SandboxID: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	if queue.code != "INVALID_RESOURCE_SPEC" {
		t.Fatalf("completion code = %q, want INVALID_RESOURCE_SPEC", queue.code)
	}
}

func (q *rejectingQueue) ClaimSandboxLifecycle(context.Context, string, string) (operation.LifecycleJob, error) {
	return operation.LifecycleJob{}, operation.ErrNoLifecycleOperation
}
func (q *rejectingQueue) ExtendSandboxLifecycleLease(context.Context, string, string) error {
	return nil
}
func (q *rejectingQueue) CompleteSandboxLifecycle(context.Context, operation.LifecycleJob, string) error {
	return nil
}
func (q *rejectingQueue) RetrySandboxLifecycle(context.Context, operation.LifecycleJob) error {
	return nil
}
