package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type nodeProbeResult struct {
	SandboxID        string `json:"sandbox_id"`
	BootID           string `json:"boot_id"`
	GuestEpoch       string `json:"guest_epoch"`
	ReplayVerified   bool   `json:"replay_verified"`
	CapacityReleased bool   `json:"capacity_released"`
	Deleted          bool   `json:"deleted"`
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
	directory, err := os.MkdirTemp("", "isodo-node-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(directory)
	server, err := orchestrator.NewCHVServer(orchestrator.NodeStatus{NodeID: "validation-node", BootID: uuid.NewString(), TotalVCPU: 2, AvailableVCPU: 2, TotalMemoryBytes: 512 << 20, AvailableMemoryBytes: 512 << 20}, orchestrator.CHVConfig{Directory: directory, CacheDirectory: cache, Binary: binary, Bucket: bucket, Fetcher: storage})
	if err != nil {
		return result, err
	}
	defer server.Close()
	request := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), SandboxId: uuid.NewString(), OwnershipGeneration: 1, Revision: 1, Action: nodev1.OperationAction_OPERATION_ACTION_CREATE, TemplateId: templateID, TemplateBuildId: buildID, TemplateArtifact: &nodev1.TemplateArtifact{ManifestDigest: digest, Bucket: bucket, Prefix: "templates/sha256/" + digest}, Resources: &nodev1.ResourceSpec{Vcpu: 2, MemoryBytes: 512 << 20}, Network: &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_DENY}}
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
	result.SandboxID = request.SandboxId
	result.BootID = inspection.BootId
	result.GuestEpoch = inspection.GuestEpoch
	replay, err := server.ApplyOperation(ctx, request)
	if err != nil || !replay.AlreadyApplied {
		return result, errors.New("Node did not recognize a durable operation replay")
	}
	result.ReplayVerified = true
	for i, action := range []nodev1.OperationAction{nodev1.OperationAction_OPERATION_ACTION_STOP, nodev1.OperationAction_OPERATION_ACTION_DELETE} {
		operation := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), SandboxId: request.SandboxId, OwnershipGeneration: 1, Revision: uint64(i + 2), Action: action}
		if err = apply(operation); err != nil {
			return result, err
		}
	}
	inspection, err = server.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{SandboxId: request.SandboxId, OwnershipGeneration: 1})
	if err != nil {
		return result, err
	}
	result.Deleted = inspection.ObservedState == "deleted"
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
