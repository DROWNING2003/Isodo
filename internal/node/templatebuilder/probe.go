// Package templatebuilder manages short-lived Linux/KVM template build VMs.
package templatebuilder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

type ProbeResult struct {
	AgentVersion            string `json:"agent_version"`
	BootToReadyMilliseconds int64  `json:"boot_to_ready_ms"`
	TotalMilliseconds       int64  `json:"total_ms"`
}

// Probe verifies a fresh VM from copies of the artifact. It does not publish a
// Registry build, and the candidate's immutable files are never passed writable.
func Probe(ctx context.Context, binary, directory, digest string) (ProbeResult, error) {
	var result ProbeResult
	if runtime.GOOS != "linux" {
		return result, errors.New("template VMs require Linux/KVM")
	}
	start := time.Now()
	directory, err := filepath.Abs(directory)
	if err != nil {
		return result, err
	}
	m, err := templateartifact.Verify(directory, digest)
	if err != nil {
		return result, err
	}
	if m.MinimumMemoryMiB > 4096 {
		return result, errors.New("template exceeds the 4 GiB validation VM limit")
	}
	work, err := os.MkdirTemp("", "isodo-probe-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(work)
	config, err := prepareVM(ctx, directory, work, m)
	if err != nil {
		return result, err
	}

	session, err := boot(ctx, binary, work, config)
	if err != nil {
		return result, err
	}
	defer session.kill()
	readyStart := time.Now()
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ready, err := guesttransport.WaitTemplateReady(readyCtx, config.Vsock.Socket)
	if err != nil {
		return result, withLogTail(err, config.Serial.File)
	}
	if err = verifyReady(ready, m); err != nil {
		return result, err
	}
	result.AgentVersion = ready.AgentVersion
	result.BootToReadyMilliseconds = time.Since(readyStart).Milliseconds()
	if err = session.stop(ctx); err != nil {
		return result, withLogTail(err, config.Serial.File)
	}
	result.TotalMilliseconds = time.Since(start).Milliseconds()
	return result, nil
}
func prepareVM(ctx context.Context, directory, work string, m templateartifact.Manifest) (vmm.VMConfig, error) {
	rootfs := filepath.Join(work, "rootfs.raw")
	if err := copyDisk(ctx, filepath.Join(directory, m.RootFS.Name), rootfs); err != nil {
		return vmm.VMConfig{}, err
	}
	workspace := filepath.Join(work, "workspace.raw")
	disk, err := os.OpenFile(workspace, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return vmm.VMConfig{}, err
	}
	truncateErr := disk.Truncate(m.Workspace.MinimumSizeBytes)
	closeErr := disk.Close()
	if err = errors.Join(truncateErr, closeErr); err != nil {
		return vmm.VMConfig{}, err
	}
	if output, err := exec.CommandContext(ctx, "mkfs.ext4", "-F", "-q", "-m", "0", workspace).CombinedOutput(); err != nil {
		return vmm.VMConfig{}, fmt.Errorf("format validation workspace: %w: %s", err, output)
	}
	config := vmm.VMConfig{CPUs: vmm.CPUConfig{BootVCPUs: 2, MaxVCPUs: 2}, Memory: vmm.MemoryConfig{Size: uint64(m.MinimumMemoryMiB) << 20}, Payload: vmm.PayloadConfig{Kernel: filepath.Join(directory, m.Kernel.Name), Cmdline: "console=ttyS0 root=/dev/vda rw rdinit=/init panic=-1 random.trust_cpu=on"}, Disks: []vmm.DiskConfig{{Path: rootfs, ImageType: "Raw"}, {Path: workspace, ImageType: "Raw"}}, Vsock: vmm.VsockConfig{CID: 3, Socket: filepath.Join(work, "vsock")}, Serial: vmm.ConsoleConfig{Mode: "File", File: filepath.Join(work, "serial.log")}, Console: vmm.ConsoleConfig{Mode: "Off"}}
	if m.Initramfs != nil {
		config.Payload.Initramfs = filepath.Join(directory, m.Initramfs.Name)
	}
	return config, nil
}

func verifyReady(ready *guestv1.ReadyResponse, m templateartifact.Manifest) error {
	if ready == nil || ready.RuntimeBound || ready.AgentVersion != m.GuestAgent.Version || ready.ProtocolMajor != m.GuestProtocol.Major || ready.ProtocolMinor < m.GuestProtocol.MinimumMinor {
		return errors.New("Guest Ready does not match the manifest")
	}
	for _, capability := range m.GuestAgent.Capabilities {
		if !slices.Contains(ready.Capabilities, capability) {
			return fmt.Errorf("Guest is missing capability %s", capability)
		}
	}
	return nil
}
func copyDisk(ctx context.Context, source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	cloned, cloneErr := tryClone(output, input)
	closeErr := output.Close()
	if cloneErr != nil || closeErr != nil {
		return errors.Join(cloneErr, closeErr)
	}
	if cloned {
		return nil
	}
	outputBytes, err := exec.CommandContext(ctx, "cp", "--sparse=always", "--reflink=never", "--", source, destination).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy template disk: %w: %s", err, outputBytes)
	}
	return nil
}

type vmSession struct {
	command *exec.Cmd
	client  *vmm.Client
	done    chan struct{}
	waitErr error
	log     *os.File
}

func boot(ctx context.Context, binary, work string, config vmm.VMConfig) (*vmSession, error) {
	return bootWithLifetime(ctx, ctx, binary, work, config)
}

func bootWithLifetime(ctx, lifetime context.Context, binary, work string, config vmm.VMConfig) (*vmSession, error) {
	return bootWithSockets(ctx, lifetime, binary, work, work, config)
}
func bootWithSockets(ctx, lifetime context.Context, binary, work, sockets string, config vmm.VMConfig) (*vmSession, error) {
	session, err := startVMMAt(ctx, lifetime, binary, work, sockets)
	if err != nil {
		return nil, err
	}
	if err = session.client.Create(ctx, config); err == nil {
		err = session.client.Boot(ctx)
	}
	if err != nil {
		session.kill()
		return nil, withLogTail(err, filepath.Join(work, "vmm.log"))
	}
	return session, nil
}
func startVMM(ctx, lifetime context.Context, binary, work string) (*vmSession, error) {
	return startVMMAt(ctx, lifetime, binary, work, work)
}
func startVMMAt(ctx, lifetime context.Context, binary, work, sockets string) (*vmSession, error) {
	log, err := os.OpenFile(filepath.Join(work, "vmm.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(lifetime, binary, "--api-socket", filepath.Join(sockets, "api.sock"))
	command.Stdout = log
	command.Stderr = log
	if err = command.Start(); err != nil {
		log.Close()
		return nil, err
	}
	client, err := vmm.NewClient(filepath.Join(sockets, "api.sock"))
	if err != nil {
		command.Process.Kill()
		command.Wait()
		log.Close()
		return nil, err
	}
	session := &vmSession{command: command, client: client, done: make(chan struct{}), log: log}
	go func() { session.waitErr = command.Wait(); close(session.done) }()
	apiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if err = client.Ping(apiCtx); err == nil {
			break
		}
		select {
		case <-session.done:
			session.kill()
			return nil, withLogTail(fmt.Errorf("VMM exited during startup: %w", session.waitErr), filepath.Join(work, "vmm.log"))
		case <-apiCtx.Done():
			session.kill()
			return nil, apiCtx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return session, nil
}

func (s *vmSession) kill() {
	select {
	case <-s.done:
	default:
		_ = s.command.Process.Kill()
		<-s.done
	}
	s.client.Close()
	_ = s.log.Close()
}
func (s *vmSession) stop(ctx context.Context) error {
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.client.PowerButton(stopCtx); err != nil {
		return err
	}
	for {
		info, err := s.client.Info(stopCtx)
		if err == nil && info.State == "Shutdown" {
			s.kill()
			return nil
		}
		select {
		case <-s.done:
			return s.waitErr
		case <-stopCtx.Done():
			return errors.New("Guest did not shut down after its power button")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func withLogTail(err error, path string) error {
	f, readErr := os.Open(path)
	if readErr != nil {
		return err
	}
	defer f.Close()
	info, readErr := f.Stat()
	if readErr != nil {
		return err
	}
	if _, readErr = f.Seek(max(0, info.Size()-8192), io.SeekStart); readErr != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, 8192))
	if readErr != nil {
		return err
	}
	return fmt.Errorf("%w; VM diagnostic log tail: %s", err, data)
}
