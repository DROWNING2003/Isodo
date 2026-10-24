package vmm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/google/uuid"
)

func TestRealGuestReadyProbe(t *testing.T) {
	if os.Getenv("ISODO_CHV_INTEGRATION") != "1" {
		t.Skip("set ISODO_CHV_INTEGRATION=1 on a Linux/KVM host to run the real VM probe")
	}
	chvPath := os.Getenv("ISODO_CHV_BINARY")
	kernelPath := os.Getenv("ISODO_GUEST_KERNEL")
	initramfsPath := os.Getenv("ISODO_GUEST_INITRAMFS")
	if chvPath == "" || kernelPath == "" || initramfsPath == "" {
		t.Fatal("ISODO_CHV_BINARY, ISODO_GUEST_KERNEL, and ISODO_GUEST_INITRAMFS are required")
	}
	for _, path := range []string{chvPath, kernelPath, initramfsPath} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("required regular file %q is unavailable", path)
		}
	}

	sandboxID := uuid.NewString()
	bootID := uuid.NewString()
	guestEpoch := uuid.NewString()
	workDir, err := os.MkdirTemp("/tmp", "isodo-vmm-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("real VM probe artifacts retained at %s", workDir)
			return
		}
		_ = os.RemoveAll(workDir)
	})
	socketPath := filepath.Join(workDir, "ch.sock")
	vsockPath := filepath.Join(workDir, "vsock.sock")
	logFile, err := os.Create(filepath.Join(workDir, "vmm.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	command := exec.Command(chvPath, "--api-socket", socketPath, "--log-file", logFile.Name())
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		t.Fatalf("start Cloud Hypervisor: %v", err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-processDone:
		case <-time.After(3 * time.Second):
		}
	})

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	started := time.Now()
	for {
		if err := client.Ping(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for VMM API: %v", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	vmmReadyAt := time.Now()
	vsockCID := uint32(5000 + time.Now().UnixNano()%50000)
	config := VMConfig{
		CPUs:   CPUConfig{BootVCPUs: 1, MaxVCPUs: 1},
		Memory: MemoryConfig{Size: 256 << 20},
		Payload: PayloadConfig{
			Kernel:    kernelPath,
			Initramfs: initramfsPath,
			Cmdline: fmt.Sprintf(
				"console=ttyS0 rdinit=/init panic=1 isodo.sandbox_id=%s isodo.ownership_generation=1 isodo.boot_id=%s isodo.guest_epoch=%s",
				sandboxID, bootID, guestEpoch,
			),
		},
		Vsock:   VsockConfig{CID: vsockCID, Socket: vsockPath},
		Serial:  ConsoleConfig{Mode: "File", File: filepath.Join(workDir, "serial.log")},
		Console: ConsoleConfig{Mode: "Off"},
	}
	createStarted := time.Now()
	if err := client.Create(ctx, config); err != nil {
		t.Fatalf("create VM: %v", err)
	}
	createDone := time.Now()
	if err := client.Boot(ctx); err != nil {
		t.Fatalf("boot VM: %v", err)
	}
	bootDone := time.Now()
	identity := &guestv1.RuntimeIdentity{
		SandboxId:           sandboxID,
		OwnershipGeneration: 1,
		BootId:              bootID,
		GuestEpoch:          guestEpoch,
	}
	if _, err := guesttransport.WaitReady(ctx, vsockPath, identity); err != nil {
		t.Fatalf("wait for real Guest Agent Ready: %v", err)
	}
	readyDone := time.Now()
	info, err := client.Info(ctx)
	if err != nil || info.State != "Running" {
		t.Fatalf("inspect running VM: state=%q err=%v", info.State, err)
	}
	t.Logf("phase_ms process_to_vmm_api=%d vm_create=%d vm_boot_call=%d boot_call_to_guest_ready=%d total_guest_ready=%d vsock_cid=%s", vmmReadyAt.Sub(started).Milliseconds(), createDone.Sub(createStarted).Milliseconds(), bootDone.Sub(createDone).Milliseconds(), readyDone.Sub(bootDone).Milliseconds(), readyDone.Sub(started).Milliseconds(), strconv.FormatUint(uint64(vsockCID), 10))
}
