package sandboxworker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/operation"
	"github.com/DROWNING2003/Isodo/internal/control/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrNoCreateOperation = operation.ErrNoCreateOperation

type Queue interface {
	ClaimSandboxCreate(context.Context, string, string) (operation.CreateJob, error)
	ExtendSandboxCreateLease(context.Context, string, string) error
	CompleteSandboxCreate(context.Context, operation.CreateJob, string) error
	RetrySandboxCreate(context.Context, operation.CreateJob) error
	ClaimSandboxLifecycle(context.Context, string, string) (operation.LifecycleJob, error)
	ExtendSandboxLifecycleLease(context.Context, string, string) error
	CompleteSandboxLifecycle(context.Context, operation.LifecycleJob, string) error
	RetrySandboxLifecycle(context.Context, operation.LifecycleJob) error
}

type Worker struct {
	queue    Queue
	client   *orchestrator.Client
	nodeID   string
	backend  string
	logger   *slog.Logger
	workerID string
}

func New(queue Queue, client *orchestrator.Client, nodeID, backend string, logger *slog.Logger) (*Worker, error) {
	if queue == nil || client == nil || strings.TrimSpace(nodeID) == "" {
		return nil, errors.New("sandbox create worker requires a queue, Node client and Node ID")
	}
	if backend == "" {
		backend = "chv"
	}
	if logger == nil {
		logger = slog.Default()
	}
	host, _ := os.Hostname()
	return &Worker{queue: queue, client: client, nodeID: nodeID, backend: backend, logger: logger, workerID: host + ":" + uuid.NewString()}, nil
}

func (w *Worker) Run(ctx context.Context) {
	nextNodeCheck := time.Time{}
	for ctx.Err() == nil {
		if time.Now().After(nextNodeCheck) {
			if _, err := w.client.GetNodeStatus(ctx); err != nil {
				if ctx.Err() == nil {
					w.logger.Error("check sandbox Node availability", "node_id", w.nodeID, "error", err)
					if !wait(ctx, 5*time.Second) {
						return
					}
				}
				nextNodeCheck = time.Now().Add(5 * time.Second)
				continue
			}
			nextNodeCheck = time.Now().Add(10 * time.Second)
		}
		job, err := w.queue.ClaimSandboxCreate(ctx, w.workerID, w.nodeID)
		if errors.Is(err, ErrNoCreateOperation) {
			lifecycle, lifecycleErr := w.queue.ClaimSandboxLifecycle(ctx, w.workerID, w.nodeID)
			if errors.Is(lifecycleErr, operation.ErrNoLifecycleOperation) {
				if !wait(ctx, time.Second) {
					return
				}
				continue
			}
			if lifecycleErr != nil {
				if ctx.Err() == nil {
					w.logger.Error("claim sandbox lifecycle operation", "error", lifecycleErr)
					if !wait(ctx, time.Second) {
						return
					}
				}
				continue
			}
			if err := w.runLifecycle(ctx, lifecycle); err != nil && ctx.Err() == nil {
				w.logger.Error("sandbox lifecycle operation deferred", "operation_id", lifecycle.OperationID, "sandbox_id", lifecycle.SandboxID, "action", lifecycle.Action, "error", err)
				if retryErr := w.queue.RetrySandboxLifecycle(ctx, lifecycle); retryErr != nil {
					w.logger.Error("release sandbox lifecycle operation", "operation_id", lifecycle.OperationID, "error", retryErr)
				}
			}
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Error("claim sandbox create operation", "error", err)
				if !wait(ctx, time.Second) {
					return
				}
			}
			continue
		}
		if err := w.runCreate(ctx, job); err != nil && ctx.Err() == nil {
			w.logger.Error("sandbox create operation deferred", "operation_id", job.OperationID, "sandbox_id", job.SandboxID, "error", err)
			if retryErr := w.queue.RetrySandboxCreate(ctx, job); retryErr != nil {
				w.logger.Error("release sandbox create operation", "operation_id", job.OperationID, "error", retryErr)
			}
		}
	}
}

func (w *Worker) runCreate(ctx context.Context, job operation.CreateJob) error {
	if w.backend == "lima" && job.TemplateRef != sandbox.LocalLimaTemplateReference && job.TemplateRef != "linux-base" {
		return w.queue.CompleteSandboxCreate(ctx, job, "CAPABILITY_UNSUPPORTED")
	}
	if w.backend != "lima" && (job.Resources.VCPU == 0 || job.Resources.MemoryBytes == 0) {
		return w.queue.CompleteSandboxCreate(ctx, job, "INVALID_RESOURCE_SPEC")
	}
	request := createRequest(job, w.backend)
	if w.backend == "lima" {
		// The local Lima provider clones its Guest-Agent/T3-enabled Base. It does
		// not materialize object-store template builds.
	} else {
		request.TemplateId = job.TemplateID
		request.TemplateBuildId = job.TemplateBuildID
		request.TemplateArtifact = &nodev1.TemplateArtifact{
			ManifestDigest: job.TemplateDigest,
			Bucket:         job.ArtifactBucket,
			Prefix:         job.ArtifactPrefix,
		}
	}

	if _, err := w.client.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: job.OperationID, RequestHash: job.RequestHash}); err != nil {
		if status.Code(err) != codes.NotFound {
			return err
		}
		if _, err := w.client.ApplyOperation(ctx, request); err != nil {
			switch status.Code(err) {
			case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied, codes.ResourceExhausted:
				return w.queue.CompleteSandboxCreate(ctx, job, "NODE_OPERATION_REJECTED")
			default:
				return err
			}
		}
	}

	for {
		if err := w.queue.ExtendSandboxCreateLease(ctx, job.OperationID, w.workerID); err != nil {
			return err
		}
		current, err := w.client.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: job.OperationID, RequestHash: job.RequestHash})
		if err != nil {
			return err
		}
		switch current.GetState() {
		case nodev1.OperationState_OPERATION_STATE_SUCCEEDED:
			return w.queue.CompleteSandboxCreate(ctx, job, "")
		case nodev1.OperationState_OPERATION_STATE_FAILED:
			code := current.GetErrorCode()
			if code == "" {
				code = "NODE_OPERATION_FAILED"
			}
			return w.queue.CompleteSandboxCreate(ctx, job, code)
		}
		if !wait(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
}

func createRequest(job operation.CreateJob, backend string) *nodev1.ApplyOperationRequest {
	request := &nodev1.ApplyOperationRequest{
		OperationId:         job.OperationID,
		RequestHash:         job.RequestHash,
		SandboxId:           job.SandboxID,
		OwnershipGeneration: job.OwnershipGeneration,
		Revision:            job.Revision,
		Action:              nodev1.OperationAction_OPERATION_ACTION_CREATE,
	}
	if backend != "lima" {
		request.Resources = &nodev1.ResourceSpec{Vcpu: job.Resources.VCPU, MemoryBytes: job.Resources.MemoryBytes}
		request.Network = &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_ALLOW}
	}
	return request
}

func (w *Worker) runLifecycle(ctx context.Context, job operation.LifecycleJob) error {
	action := nodev1.OperationAction_OPERATION_ACTION_STOP
	if job.Action == "start" {
		action = nodev1.OperationAction_OPERATION_ACTION_START
	}
	request := &nodev1.ApplyOperationRequest{
		OperationId: job.OperationID, RequestHash: job.RequestHash, SandboxId: job.SandboxID,
		OwnershipGeneration: job.OwnershipGeneration, Revision: job.Revision, Action: action,
	}
	if _, err := w.client.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: job.OperationID, RequestHash: job.RequestHash}); err != nil {
		if status.Code(err) != codes.NotFound {
			return err
		}
		if _, err := w.client.ApplyOperation(ctx, request); err != nil {
			switch status.Code(err) {
			case codes.InvalidArgument, codes.FailedPrecondition, codes.PermissionDenied, codes.ResourceExhausted, codes.NotFound:
				return w.queue.CompleteSandboxLifecycle(ctx, job, "NODE_OPERATION_REJECTED")
			default:
				return err
			}
		}
	}
	for {
		if err := w.queue.ExtendSandboxLifecycleLease(ctx, job.OperationID, w.workerID); err != nil {
			return err
		}
		current, err := w.client.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: job.OperationID, RequestHash: job.RequestHash})
		if err != nil {
			return err
		}
		switch current.GetState() {
		case nodev1.OperationState_OPERATION_STATE_SUCCEEDED:
			return w.queue.CompleteSandboxLifecycle(ctx, job, "")
		case nodev1.OperationState_OPERATION_STATE_FAILED:
			code := current.GetErrorCode()
			if code == "" {
				code = "NODE_OPERATION_FAILED"
			}
			return w.queue.CompleteSandboxLifecycle(ctx, job, code)
		}
		if !wait(ctx, 250*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
