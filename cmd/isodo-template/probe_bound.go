package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type boundProbeResult struct {
	Identities                []*guestv1.RuntimeIdentity `json:"identities"`
	ReadyMilliseconds         []int64                    `json:"boot_to_bound_ready_ms"`
	CreateDeadlineIndependent bool                       `json:"create_deadline_independent"`
	CrossBindRejected         bool                       `json:"cross_bind_rejected"`
	ShutdownVerified          bool                       `json:"shutdown_verified"`
	StorageVerified           bool                       `json:"storage_verified"`
}

func probeBound(ctx context.Context, binary, directory, digest string) (boundProbeResult, error) {
	var result boundProbeResult
	parent, err := os.MkdirTemp("", "isodo-bound-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(parent)
	var instances []*templatebuilder.Runtime
	defer func() {
		for _, instance := range instances {
			_ = instance.Delete()
		}
	}()
	for range 2 {
		identity := &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: uuid.NewString()}
		createCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		instance, err := templatebuilder.StartRuntime(createCtx, ctx, templatebuilder.RuntimeConfig{VMMBinary: binary, ArtifactDirectory: directory, ManifestDigest: digest, Directory: filepath.Join(parent, identity.SandboxId), Identity: identity})
		cancel() // A finished create RPC must not kill the VM process.
		if err != nil {
			return result, err
		}
		instances = append(instances, instance)
		ready, err := guesttransport.WaitReady(ctx, instance.VsockSocket(), instance.Identity())
		if err != nil {
			return result, err
		}
		result.Identities = append(result.Identities, ready.Identity)
		result.ReadyMilliseconds = append(result.ReadyMilliseconds, instance.BootToReadyMilliseconds())
	}
	result.CreateDeadlineIndependent = true
	if result.Identities[0].GuestEpoch == result.Identities[1].GuestEpoch {
		return result, errors.New("two VMs share a Guest epoch")
	}
	_, err = guesttransport.BindRuntime(ctx, instances[0].VsockSocket(), instances[1].Identity())
	if status.Code(err) != codes.FailedPrecondition {
		return result, errors.New("cross-runtime rebind was not refused by the Guest")
	}
	result.CrossBindRejected = true
	for _, instance := range instances {
		if _, err := guesttransport.BindRuntime(ctx, instance.VsockSocket(), instance.Identity()); err != nil {
			return result, err
		}
		if err := instance.Stop(ctx); err != nil {
			return result, err
		}
	}
	result.ShutdownVerified = true
	return result, nil
}
