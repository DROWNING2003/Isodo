package templatebuilder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// RuntimeConfig is Node-owned input. No host path comes from the public API.
type RuntimeConfig struct {
	VMMBinary, ArtifactDirectory, ManifestDigest, Directory string
	Identity                                                *guestv1.RuntimeIdentity
	CPUs                                                    uint32
	MemoryBytes                                             uint64
	DiskBytes                                               uint64
	Tap, MAC, GuestIP, Gateway                              string
	Verified                                                *templateartifact.Artifact
}

// Runtime owns one fresh VM and its independent writable disks. Its process is
// tied to the Node lifetime, not the create RPC deadline. The caller must Close.
type Runtime struct {
	session           *vmSession
	directory, vsock  string
	identity          *guestv1.RuntimeIdentity
	readyMilliseconds int64
}

func StartRuntime(ctx, lifetime context.Context, config RuntimeConfig) (_ *Runtime, err error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("template VMs require Linux/KVM")
	}
	if config.Identity == nil || config.Identity.SandboxId == "" || config.Identity.OwnershipGeneration == 0 || config.Identity.BootId == "" || config.Identity.GuestEpoch != "" {
		return nil, errors.New("new runtime requires sandbox ID, generation and boot ID, without a guest epoch")
	}
	if config.Directory == "" || config.VMMBinary == "" {
		return nil, errors.New("runtime directory and VMM binary are required")
	}
	if err = uuid.Validate(config.Identity.SandboxId); err != nil {
		return nil, errors.New("runtime sandbox ID must be a UUID")
	}
	artifact, err := filepath.Abs(config.ArtifactDirectory)
	if err != nil {
		return nil, err
	}
	directory, err := filepath.Abs(config.Directory)
	if err != nil {
		return nil, err
	}
	var m templateartifact.Manifest
	if config.Verified != nil {
		if config.Verified.Digest() != config.ManifestDigest || config.Verified.Directory() != artifact {
			return nil, errors.New("verification receipt does not match runtime artifact")
		}
		err = config.Verified.Check()
		if err == nil {
			m = config.Verified.Manifest()
		}
	} else {
		m, err = templateartifact.Verify(artifact, config.ManifestDigest)
	}
	if err != nil {
		return nil, err
	}
	if config.CPUs == 0 {
		config.CPUs = 2
	}
	if config.MemoryBytes == 0 {
		config.MemoryBytes = uint64(m.MinimumMemoryMiB) << 20
	}
	if config.CPUs > 64 || config.MemoryBytes < uint64(m.MinimumMemoryMiB)<<20 || config.MemoryBytes > 4<<30 {
		return nil, errors.New("runtime resources are outside supported limits")
	}
	// Reuse is forbidden: a second operation must never format another VM's disk.
	if config.DiskBytes != 0 && config.DiskBytes != uint64(m.Workspace.MinimumSizeBytes) {
		return nil, errors.New("workspace resizing is not supported by this runtime")
	}
	if m.RuntimeSnapshot != nil {
		return restoreArtifactRuntime(ctx, lifetime, config, artifact, directory, m)
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return nil, fmt.Errorf("reserve runtime directory: %w", err)
	}
	result := &Runtime{directory: directory}
	success := false
	defer func() {
		if !success {
			result.Close()
			_ = os.RemoveAll(directory)
		}
	}()
	vmConfig, err := prepareVM(ctx, artifact, directory, m)
	if err != nil {
		return nil, err
	}
	vmConfig.CPUs.BootVCPUs = config.CPUs
	vmConfig.CPUs.MaxVCPUs = config.CPUs
	vmConfig.Memory.Size = config.MemoryBytes
	attachNetwork(&vmConfig, config)
	// No network device is attached: egress is denied until network isolation is wired.
	result.vsock = vmConfig.Vsock.Socket
	result.session, err = bootWithLifetime(ctx, lifetime, config.VMMBinary, directory, vmConfig)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ready, err := guesttransport.WaitTemplateReady(readyCtx, result.vsock)
	if err != nil {
		return nil, withLogTail(err, vmConfig.Serial.File)
	}
	if err = verifyReady(ready, m); err != nil {
		return nil, err
	}
	bound, err := guesttransport.BindRuntime(readyCtx, result.vsock, config.Identity)
	if err != nil {
		return nil, err
	}
	expected := proto.Clone(bound.Identity).(*guestv1.RuntimeIdentity)
	ready, err = guesttransport.WaitReady(readyCtx, result.vsock, expected)
	if err != nil {
		return nil, err
	}
	result.identity = proto.Clone(ready.Identity).(*guestv1.RuntimeIdentity)
	result.readyMilliseconds = time.Since(started).Milliseconds()
	success = true
	return result, nil
}

func attachNetwork(config *vmm.VMConfig, runtime RuntimeConfig) {
	if runtime.Tap == "" {
		return
	}
	config.Net = []vmm.NetConfig{{Tap: runtime.Tap, Mac: runtime.MAC, NumQueues: 2, QueueSize: 256}}
	if runtime.GuestIP != "" && runtime.Gateway != "" {
		config.Payload.Cmdline += " isodo.net=" + runtime.GuestIP + "," + runtime.Gateway
	}
}

// RestartRuntime cold-boots an existing Sandbox directory with its persisted
// rootfs/workspace disks and a new boot identity. It never copies or formats
// the instance disks; a failed start leaves them intact for recovery.
func RestartRuntime(ctx, lifetime context.Context, config RuntimeConfig) (_ *Runtime, err error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("template VMs require Linux/KVM")
	}
	if config.Identity == nil || config.Identity.SandboxId == "" || config.Identity.OwnershipGeneration == 0 || config.Identity.BootId == "" || config.Identity.GuestEpoch != "" {
		return nil, errors.New("restart requires sandbox ID, generation and a new boot ID, without a guest epoch")
	}
	if config.Directory == "" || config.VMMBinary == "" {
		return nil, errors.New("runtime directory and VMM binary are required")
	}
	if uuid.Validate(config.Identity.SandboxId) != nil {
		return nil, errors.New("runtime sandbox ID must be a UUID")
	}
	artifact, err := filepath.Abs(config.ArtifactDirectory)
	if err != nil {
		return nil, err
	}
	directory, err := filepath.Abs(config.Directory)
	if err != nil {
		return nil, err
	}
	var m templateartifact.Manifest
	if config.Verified != nil {
		if config.Verified.Digest() != config.ManifestDigest || config.Verified.Directory() != artifact {
			return nil, errors.New("verification receipt does not match runtime artifact")
		}
		if err = config.Verified.Check(); err != nil {
			return nil, err
		}
		m = config.Verified.Manifest()
	} else {
		m, err = templateartifact.Verify(artifact, config.ManifestDigest)
		if err != nil {
			return nil, err
		}
	}
	if config.CPUs == 0 {
		config.CPUs = 2
	}
	if config.MemoryBytes == 0 {
		config.MemoryBytes = uint64(m.MinimumMemoryMiB) << 20
	}
	if config.CPUs > 64 || config.MemoryBytes < uint64(m.MinimumMemoryMiB)<<20 || config.MemoryBytes > 4<<30 {
		return nil, errors.New("runtime resources are outside supported limits")
	}
	for _, disk := range []string{filepath.Join(directory, "rootfs.raw"), filepath.Join(directory, "workspace.raw")} {
		info, statErr := os.Stat(disk)
		if statErr != nil {
			return nil, fmt.Errorf("persisted sandbox disk %s: %w", filepath.Base(disk), statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("persisted sandbox disk %s is not a regular file", filepath.Base(disk))
		}
	}
	vmConfig := vmm.VMConfig{
		CPUs:    vmm.CPUConfig{BootVCPUs: config.CPUs, MaxVCPUs: config.CPUs},
		Memory:  vmm.MemoryConfig{Size: config.MemoryBytes},
		Payload: vmm.PayloadConfig{Kernel: filepath.Join(artifact, m.Kernel.Name), Cmdline: "console=ttyS0 root=/dev/vda rw rdinit=/init panic=-1 random.trust_cpu=on"},
		Disks:   []vmm.DiskConfig{{Path: filepath.Join(directory, "rootfs.raw"), ImageType: "Raw"}, {Path: filepath.Join(directory, "workspace.raw"), ImageType: "Raw"}},
		Vsock:   vmm.VsockConfig{CID: 3, Socket: filepath.Join(directory, "vsock")},
		Serial:  vmm.ConsoleConfig{Mode: "File", File: filepath.Join(directory, "serial.log")},
		Console: vmm.ConsoleConfig{Mode: "Off"},
	}
	if m.Initramfs != nil {
		vmConfig.Payload.Initramfs = filepath.Join(artifact, m.Initramfs.Name)
	}
	attachNetwork(&vmConfig, config)
	for _, name := range []string{"api.sock", "vsock", "vmm.log", "serial.log"} {
		if removeErr := os.Remove(filepath.Join(directory, name)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, fmt.Errorf("clear stale runtime file %s: %w", name, removeErr)
		}
	}
	result := &Runtime{directory: directory, vsock: vmConfig.Vsock.Socket}
	success := false
	defer func() {
		if !success {
			result.Close()
		}
	}()
	result.session, err = bootWithLifetime(ctx, lifetime, config.VMMBinary, directory, vmConfig)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ready, err := guesttransport.WaitTemplateReady(readyCtx, result.vsock)
	if err != nil {
		return nil, withLogTail(err, vmConfig.Serial.File)
	}
	if err = verifyReady(ready, m); err != nil {
		return nil, err
	}
	bound, err := guesttransport.BindRuntime(readyCtx, result.vsock, config.Identity)
	if err != nil {
		return nil, err
	}
	expected := proto.Clone(bound.Identity).(*guestv1.RuntimeIdentity)
	ready, err = guesttransport.WaitReady(readyCtx, result.vsock, expected)
	if err != nil {
		return nil, err
	}
	result.identity = proto.Clone(ready.Identity).(*guestv1.RuntimeIdentity)
	result.readyMilliseconds = time.Since(started).Milliseconds()
	success = true
	return result, nil
}

func (r *Runtime) Identity() *guestv1.RuntimeIdentity {
	return proto.Clone(r.identity).(*guestv1.RuntimeIdentity)
}
func (r *Runtime) VsockSocket() string            { return r.vsock }
func (r *Runtime) BootToReadyMilliseconds() int64 { return r.readyMilliseconds }
func (r *Runtime) Close() {
	if r != nil && r.session != nil {
		r.session.kill()
	}
}
func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil || r.session == nil {
		return errors.New("runtime is not initialized")
	}
	return r.session.stop(ctx)
}

// Delete removes only this Runtime's reserved directory after reaping its VMM.
func (r *Runtime) Delete() error { r.Close(); return os.RemoveAll(r.directory) }

func (r *Runtime) Running(ctx context.Context) (bool, error) {
	select {
	case <-r.session.done:
		return false, nil
	default:
	}
	info, err := r.session.client.Info(ctx)
	return err == nil && info.State == "Running", err
}
