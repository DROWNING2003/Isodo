package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
)

type artifactCloneResult struct {
	SandboxID                 string `json:"sandbox_id"`
	GuestEpoch                string `json:"guest_epoch"`
	CreateToReadyMilliseconds int64  `json:"create_to_ready_ms"`
	FirstExecMilliseconds     int64  `json:"first_exec_ms"`
	CreateToExecMilliseconds  int64  `json:"create_to_first_exec_ms"`
	Random                    string `json:"random_sample"`
}
type artifactProbeResult struct {
	Snapshot                 bool                  `json:"runtime_snapshot"`
	VerificationMilliseconds int64                 `json:"verification_ms"`
	Clones                   []artifactCloneResult `json:"clones"`
	IsolationVerified        bool                  `json:"disk_markers_isolated"`
}

func probeArtifact(ctx context.Context, binary, directory, digest string) (result artifactProbeResult, err error) {
	start := time.Now()
	artifact, err := templateartifact.VerifyArtifact(directory, digest)
	if err != nil {
		return result, err
	}
	result.VerificationMilliseconds = time.Since(start).Milliseconds()
	result.Snapshot = artifact.Manifest().RuntimeSnapshot != nil
	parent, err := os.MkdirTemp(filepath.Dir(artifact.Directory()), ".runtime-probe-")
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
		started := time.Now()
		identity := &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: uuid.NewString()}
		createCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		instance, e := templatebuilder.StartRuntime(createCtx, ctx, templatebuilder.RuntimeConfig{VMMBinary: binary, ArtifactDirectory: artifact.Directory(), ManifestDigest: digest, Verified: artifact, Directory: filepath.Join(parent, identity.SandboxId), Identity: identity})
		cancel()
		if e != nil {
			return result, e
		}
		instances = append(instances, instance)
		row := artifactCloneResult{SandboxID: identity.SandboxId, GuestEpoch: instance.Identity().GuestEpoch, CreateToReadyMilliseconds: time.Since(started).Milliseconds()}
		commandStart := time.Now()
		row.Random, err = templatebuilder.ProbeRuntimeCommand(ctx, instance, true)
		if err != nil {
			return result, err
		}
		row.FirstExecMilliseconds = time.Since(commandStart).Milliseconds()
		row.CreateToExecMilliseconds = time.Since(started).Milliseconds()
		result.Clones = append(result.Clones, row)
	}
	if result.Clones[0].GuestEpoch == result.Clones[1].GuestEpoch || result.Clones[0].Random == result.Clones[1].Random {
		return result, errors.New("runtime clones reused random values")
	}
	for _, instance := range instances {
		if _, err = templatebuilder.ProbeRuntimeCommand(ctx, instance, false); err != nil {
			return result, err
		}
		if err = instance.Stop(ctx); err != nil {
			return result, err
		}
	}
	result.IsolationVerified = true
	return result, nil
}
