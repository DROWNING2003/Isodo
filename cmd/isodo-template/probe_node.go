package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type nodeProbeCloneResult struct {
	SandboxID                 string `json:"sandbox_id"`
	BootID                    string `json:"boot_id"`
	GuestEpoch                string `json:"guest_epoch"`
	CreateToReadyMilliseconds int64  `json:"create_to_ready_ms"`
	CreateToFirstExecMillis   int64  `json:"create_to_first_exec_ms"`
	ProbeRuntimeCommandOK     bool   `json:"probe_runtime_command_ok"`
}

type nodeProbeResult struct {
	SandboxID        string                 `json:"sandbox_id"`
	BootID           string                 `json:"boot_id"`
	GuestEpoch       string                 `json:"guest_epoch"`
	ReplayVerified   bool                   `json:"replay_verified"`
	CapacityReleased bool                   `json:"capacity_released"`
	Deleted          bool                   `json:"deleted"`
	Clones           []nodeProbeCloneResult `json:"clones,omitempty"`
	CacheReused      bool                   `json:"cache_reused"`
}

type probeVerifiedFetcher struct {
	store *templateartifact.S3Store

	mu       sync.Mutex
	receipts []*templateartifact.Artifact
}

func (f *probeVerifiedFetcher) Fetch(ctx context.Context, cacheRoot, digest string) (string, error) {
	return f.store.Fetch(ctx, cacheRoot, digest)
}

func (f *probeVerifiedFetcher) FetchVerified(ctx context.Context, cacheRoot, digest string) (*templateartifact.Artifact, error) {
	artifact, err := f.store.FetchVerified(ctx, cacheRoot, digest)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.receipts = append(f.receipts, artifact)
	f.mu.Unlock()
	return artifact, nil
}

func (f *probeVerifiedFetcher) cacheReused() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.receipts) == 2 && f.receipts[0] != nil && f.receipts[0] == f.receipts[1]
}

func probeNode(ctx context.Context, binary, cache, digest, templateID, buildID string) (nodeProbeResult, error) {
	var result nodeProbeResult
	if err := uuid.Validate(templateID); err != nil {
		return result, errors.New("template ID is required")
	}
	if err := uuid.Validate(buildID); err != nil {
		return result, errors.New("build ID is required")
	}
	bucket := os.Getenv("ISODO_TEMPLATE_S3_BUCKET")
	storage, err := templateartifact.NewS3Store(templateartifact.S3Config{Endpoint: os.Getenv("ISODO_TEMPLATE_S3_ENDPOINT"), Region: os.Getenv("ISODO_TEMPLATE_S3_REGION"), Bucket: bucket, AccessKey: os.Getenv("SEAWEEDFS_READ_ACCESS_KEY"), SecretKey: os.Getenv("SEAWEEDFS_READ_SECRET_KEY")})
	if err != nil {
		return result, err
	}
	verifiedFetcher := &probeVerifiedFetcher{store: storage}
	directory, err := os.MkdirTemp("", "isodo-node-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(directory)
	runtimes := make(map[string]*templatebuilder.Runtime)
	var runtimesMu sync.Mutex
	server, err := orchestrator.NewCHVServer(orchestrator.NodeStatus{NodeID: "validation-node", BootID: uuid.NewString(), TotalVCPU: 2, AvailableVCPU: 2, TotalMemoryBytes: 512 << 20, AvailableMemoryBytes: 512 << 20}, orchestrator.CHVConfig{
		Directory:      directory,
		CacheDirectory: cache,
		Binary:         binary,
		Bucket:         bucket,
		Fetcher:        verifiedFetcher,
		Launch: func(launchCtx, lifetime context.Context, config templatebuilder.RuntimeConfig) (orchestrator.CHVRuntime, error) {
			runtime, err := templatebuilder.StartRuntime(launchCtx, lifetime, config)
			if err == nil {
				runtimesMu.Lock()
				runtimes[config.Identity.SandboxId] = runtime
				runtimesMu.Unlock()
			}
			return runtime, err
		},
	})
	if err != nil {
		return result, err
	}
	defer server.Close()

	apply := func(request *nodev1.ApplyOperationRequest) error {
		data, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		request.RequestHash = hex.EncodeToString(sum[:])
		if _, err = server.ApplyOperation(ctx, request); err != nil {
			return err
		}
		for {
			op, err := server.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: request.OperationId, RequestHash: request.RequestHash})
			if err != nil {
				return err
			}
			if op.State == nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
				return nil
			}
			if op.State == nodev1.OperationState_OPERATION_STATE_FAILED {
				return fmt.Errorf("Node operation failed: %s", op.ErrorCode)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}

	for cloneIndex := range 2 {
		started := time.Now()
		request := &nodev1.ApplyOperationRequest{
			OperationId: uuid.NewString(), SandboxId: uuid.NewString(), OwnershipGeneration: 1, Revision: 1,
			Action: nodev1.OperationAction_OPERATION_ACTION_CREATE, TemplateId: templateID, TemplateBuildId: buildID,
			TemplateArtifact: &nodev1.TemplateArtifact{ManifestDigest: digest, Bucket: bucket, Prefix: "templates/sha256/" + digest},
			Resources:        &nodev1.ResourceSpec{Vcpu: 2, MemoryBytes: 512 << 20}, Network: &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_DENY},
		}
		if err = apply(request); err != nil {
			return result, err
		}
		inspection, err := server.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: request.SandboxId, OwnershipGeneration: 1})
		if err != nil {
			return result, err
		}
		if inspection.ObservedState != "running" || inspection.BootId == "" || inspection.GuestEpoch == "" {
			return result, errors.New("Node did not inspect a running bound Guest")
		}
		readyAt := time.Since(started).Milliseconds()
		runtimesMu.Lock()
		runtime := runtimes[request.SandboxId]
		runtimesMu.Unlock()
		if runtime == nil {
			return result, errors.New("Node did not retain the created runtime")
		}
		if _, err = templatebuilder.ProbeRuntimeCommand(ctx, runtime, true); err != nil {
			return result, err
		}
		clone := nodeProbeCloneResult{SandboxID: request.SandboxId, BootID: inspection.BootId, GuestEpoch: inspection.GuestEpoch, CreateToReadyMilliseconds: readyAt, CreateToFirstExecMillis: time.Since(started).Milliseconds(), ProbeRuntimeCommandOK: true}
		result.Clones = append(result.Clones, clone)
		if cloneIndex == 0 {
			result.SandboxID, result.BootID, result.GuestEpoch = clone.SandboxID, clone.BootID, clone.GuestEpoch
		}
		replay, err := server.ApplyOperation(ctx, request)
		if err != nil || !replay.AlreadyApplied {
			return result, errors.New("Node did not recognize a durable operation replay")
		}
		result.ReplayVerified = true
		stop := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), SandboxId: request.SandboxId, OwnershipGeneration: 1, Revision: 2, Action: nodev1.OperationAction_OPERATION_ACTION_STOP}
		if err = apply(stop); err != nil {
			return result, err
		}
		deleteRequest := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), SandboxId: request.SandboxId, OwnershipGeneration: 1, Revision: 3, Action: nodev1.OperationAction_OPERATION_ACTION_DELETE}
		if err = apply(deleteRequest); err != nil {
			return result, err
		}
		inspection, err = server.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: request.SandboxId, OwnershipGeneration: 1})
		if err != nil {
			return result, err
		}
		if inspection.ObservedState != "deleted" {
			return result, errors.New("Node cleanup did not delete the runtime")
		}
		result.Deleted = true
	}
	if len(result.Clones) != 2 || result.Clones[0].BootID == result.Clones[1].BootID || result.Clones[0].GuestEpoch == result.Clones[1].GuestEpoch {
		return result, errors.New("Node clones did not receive distinct identities")
	}
	result.CacheReused = verifiedFetcher.cacheReused()
	if !result.CacheReused {
		return result, errors.New("Node did not reuse the FetchVerified receipt")
	}
	capacity, err := server.GetNodeStatus(ctx, new(emptypb.Empty))
	if err != nil {
		return result, err
	}
	result.CapacityReleased = capacity.AvailableVcpu == 2 && capacity.AvailableMemoryBytes == 512<<20
	if !result.Deleted || !result.CapacityReleased {
		return result, errors.New("Node cleanup did not settle runtime state and capacity")
	}
	return result, nil
}
