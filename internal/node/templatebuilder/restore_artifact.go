package templatebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"google.golang.org/protobuf/proto"
)

func restoreArtifactRuntime(ctx, lifetime context.Context, config RuntimeConfig, artifact, directory string, m templateartifact.Manifest) (_ *Runtime, err error) {
	snapshot := m.RuntimeSnapshot
	if config.CPUs != snapshot.VCPU || config.MemoryBytes != snapshot.MemoryBytes {
		return nil, errors.New("requested resources do not match runtime snapshot")
	}
	cpu, err := cpuSignature()
	if err != nil {
		return nil, err
	}
	if cpu != snapshot.CPUSignature {
		return nil, errors.New("runtime snapshot CPU compatibility signature does not match this host")
	}
	binary, err := vmmDigest(config.VMMBinary)
	if err != nil {
		return nil, err
	}
	if binary != snapshot.VMMDigest {
		return nil, errors.New("runtime snapshot requires the exact CHV binary used to build it")
	}
	if _, err = exec.LookPath("qemu-img"); err != nil {
		return nil, errors.New("QCOW2 restore requires qemu-img")
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return nil, fmt.Errorf("reserve restore directory: %w", err)
	}
	instance := &Runtime{directory: directory, vsock: filepath.Join(directory, "vsock")}
	success := false
	defer func() {
		if !success {
			instance.Close()
			_ = os.RemoveAll(directory)
		}
	}()
	privateSnapshot := filepath.Join(directory, "snapshot")
	if err = os.Mkdir(privateSnapshot, 0700); err != nil {
		return nil, err
	}
	// All data remains pinned by private hard links. Cross-filesystem placement
	// fails explicitly rather than introducing a hidden full-copy path or letting
	// cache GC remove a running VM's required backing data.
	for _, pair := range [][2]string{{m.RootFS.Name, "base-root.raw"}, {snapshot.Workspace.Name, "base-workspace.raw"}, {snapshot.Memory.Name, "snapshot/memory-ranges"}, {snapshot.State.Name, "snapshot/state.json"}} {
		if err = os.Link(filepath.Join(artifact, pair[0]), filepath.Join(directory, pair[1])); err != nil {
			return nil, fmt.Errorf("pin snapshot data; cache and runtime must share a filesystem: %w", err)
		}
	}
	root, workspace := filepath.Join(directory, "root.qcow2"), filepath.Join(directory, "workspace.qcow2")
	for i, path := range []string{root, workspace} {
		backing := filepath.Join(directory, []string{"base-root.raw", "base-workspace.raw"}[i])
		output, e := exec.CommandContext(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-F", "raw", "-b", backing, path).CombinedOutput()
		if e != nil {
			return nil, fmt.Errorf("prepare private disk overlay: %w: %s", e, output)
		}
	}
	payload := vmm.PayloadConfig{Kernel: filepath.Join(artifact, m.Kernel.Name)}
	if m.Initramfs != nil {
		payload.Initramfs = filepath.Join(artifact, m.Initramfs.Name)
	}
	serial := filepath.Join(directory, "serial.log")
	if err = rewriteArtifactSnapshotConfig(filepath.Join(artifact, snapshot.Config.Name), filepath.Join(privateSnapshot, "config.json"), root, workspace, instance.vsock, serial, payload); err != nil {
		return nil, err
	}
	instance.session, err = startVMM(ctx, lifetime, config.VMMBinary, directory)
	if err != nil {
		return nil, err
	}
	if err = instance.session.client.Restore(ctx, privateSnapshot, vmm.MemoryRestoreCopyOnWrite); err != nil {
		return nil, err
	}
	confirmed, err := hasPrivateSnapshotMapping(instance.session.command.Process.Pid, config.MemoryBytes)
	if err != nil {
		return nil, err
	}
	if !confirmed {
		return nil, errors.New("CHV did not establish private CoW snapshot memory")
	}
	started := time.Now()
	if err = instance.session.client.Resume(ctx); err != nil {
		return nil, err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ready, err := guesttransport.WaitTemplateReady(readyCtx, instance.vsock)
	if err != nil {
		return nil, withLogTail(err, serial)
	}
	if err = verifyReady(ready, m); err != nil {
		return nil, err
	}
	bound, err := guesttransport.BindRuntime(readyCtx, instance.vsock, config.Identity)
	if err != nil {
		return nil, err
	}
	ready, err = guesttransport.WaitReady(readyCtx, instance.vsock, bound.Identity)
	if err != nil {
		return nil, err
	}
	instance.identity = proto.Clone(ready.Identity).(*guestv1.RuntimeIdentity)
	instance.readyMilliseconds = time.Since(started).Milliseconds()
	success = true
	return instance, nil
}

func rewriteArtifactSnapshotConfig(source, destination, root, workspace, socket, serial string, payload vmm.PayloadConfig) error {
	if err := rewriteSnapshotConfig(source, destination, root, workspace, socket, serial); err != nil {
		return err
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		return err
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	kernel, ok := config["payload"].(map[string]any)
	if !ok {
		return errors.New("snapshot has no kernel payload")
	}
	kernel["kernel"] = payload.Kernel
	if payload.Initramfs != "" {
		kernel["initramfs"] = payload.Initramfs
	} else {
		delete(kernel, "initramfs")
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err = os.Chmod(destination, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(destination, encoded, 0600); err != nil {
		return err
	}
	return os.Chmod(destination, 0400)
}
