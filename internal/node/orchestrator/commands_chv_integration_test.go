package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type commandRealRuntime struct {
	identity *guestv1.RuntimeIdentity
	socket   string
	vm       *vmm.Client
}

func (r *commandRealRuntime) Identity() *guestv1.RuntimeIdentity { return r.identity }
func (r *commandRealRuntime) VsockSocket() string                { return r.socket }
func (r *commandRealRuntime) Running(ctx context.Context) (bool, error) {
	i, e := r.vm.Info(ctx)
	return i.State == "Running", e
}
func (r *commandRealRuntime) Stop(ctx context.Context) error { return r.vm.Shutdown(ctx) }
func (r *commandRealRuntime) Close()                         {}
func (r *commandRealRuntime) Delete() error                  { return r.vm.Delete(context.Background()) }

// This test owns its VMM process, Vsock, RAM-only root and UUID. It never
// attaches to an existing Node, VM, host listener or production state directory.
func TestRealCHVRuntimeCommands(t *testing.T) {
	if os.Getenv("ISODO_CHV_INTEGRATION") != "1" {
		t.Skip("set ISODO_CHV_INTEGRATION=1 on an isolated Linux/KVM test host")
	}
	binary, kernel, initramfs := os.Getenv("ISODO_CHV_BINARY"), os.Getenv("ISODO_GUEST_KERNEL"), os.Getenv("ISODO_GUEST_INITRAMFS")
	for _, p := range []string{binary, kernel, initramfs} {
		if p == "" {
			t.Fatal("CHV binary, kernel and command-capable initramfs required")
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp("/tmp", "isodo-cmd-chv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("CHV logs retained at %s", dir)
		} else {
			_ = os.RemoveAll(dir)
		}
	})
	logFile, err := os.Create(filepath.Join(dir, "vmm.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	socket := filepath.Join(dir, "api.sock")
	vsock := filepath.Join(dir, "vsock.sock")
	process := exec.Command(binary, "--api-socket", socket)
	process.Stdout = logFile
	process.Stderr = logFile
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	t.Cleanup(func() {
		_ = process.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	vm, err := vmm.NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	for vm.Ping(ctx) != nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	node, input := commandNodeForSocket(vsock)
	id := input.SandboxID
	boot := func(revision uint64) {
		identity := &guestv1.RuntimeIdentity{SandboxId: id, OwnershipGeneration: 7, BootId: uuid.NewString(), GuestEpoch: uuid.NewString()}
		cfg := vmm.VMConfig{CPUs: vmm.CPUConfig{BootVCPUs: 1, MaxVCPUs: 1}, Memory: vmm.MemoryConfig{Size: 256 << 20},
			Payload: vmm.PayloadConfig{Kernel: kernel, Initramfs: initramfs, Cmdline: fmt.Sprintf("console=ttyS0 rdinit=/init panic=1 isodo.sandbox_id=%s isodo.ownership_generation=7 isodo.boot_id=%s isodo.guest_epoch=%s", id, identity.BootId, identity.GuestEpoch)},
			Vsock:   vmm.VsockConfig{CID: uint32(5000 + time.Now().UnixNano()%50000), Socket: vsock}, Serial: vmm.ConsoleConfig{Mode: "File", File: filepath.Join(dir, fmt.Sprintf("serial-%d.log", revision))}, Console: vmm.ConsoleConfig{Mode: "Off"}}
		if err := vm.Create(ctx, cfg); err != nil {
			t.Fatal(err)
		}
		if err := vm.Boot(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := guesttransport.WaitReady(ctx, vsock, identity); err != nil {
			t.Fatal(err)
		}
		node.live[id] = &commandRealRuntime{identity: identity, socket: vsock, vm: vm}
		node.journal.sandboxes[id] = sandboxRecord{SandboxID: id, State: "running", Generation: 7, Revision: revision, BootID: identity.BootId, GuestEpoch: identity.GuestEpoch}
	}
	boot(11)
	call := func(callCtx context.Context, key string, argv []string, timeout int) (runtimecommands.Result, error) {
		in := input
		in.Key = key
		in.Command = runtimecommands.Command{Argv: argv, TimeoutSeconds: timeout}
		out, err := node.RunCommand(callCtx, wrapperspb.Bytes(mustMarshalCommand(t, in)))
		if err != nil {
			return runtimecommands.Result{}, err
		}
		var result runtimecommands.Result
		err = json.Unmarshal(out.Value, &result)
		return result, err
	}
	argv := []string{"/bin/sh", "-c", "echo hello; echo problem >&2; exit 7"}
	first, err := call(ctx, "retry-key-0001", argv, 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stdout != "hello\n" || first.Stderr != "problem\n" || first.ExitCode == nil || *first.ExitCode != 7 {
		t.Fatalf("output=%+v", first)
	}
	again, err := call(ctx, "retry-key-0001", argv, 10)
	if err != nil || again.ExecutionID != first.ExecutionID {
		t.Fatalf("retry did not deduplicate: result=%+v err=%v", again, err)
	}
	if _, err := call(ctx, "retry-key-0001", []string{"/bin/true"}, 10); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("idempotency conflict=%v", err)
	}
	timed, err := call(ctx, "timeout-key-001", []string{"/bin/sleep", "3"}, 1)
	if err != nil || timed.Reason != "EXIT_REASON_COMMAND_TIMEOUT" || timed.ExitCode != nil {
		t.Fatalf("timeout=%+v err=%v", timed, err)
	}
	short, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = call(short, "cancel-key-0001", []string{"/bin/sh", "-c", "sleep 2; echo leaked > /workspace/cancel-marker"}, 10)
	stop()
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("cancel=%v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(2300 * time.Millisecond):
	}
	check, err := call(ctx, "check-cancel-001", []string{"/bin/sh", "-c", "test ! -e /workspace/cancel-marker"}, 10)
	if err != nil || check.ExitCode == nil || *check.ExitCode != 0 {
		t.Fatalf("canceled process survived: %+v %v", check, err)
	}
	if _, err := call(ctx, "output-cap-0001", []string{"/bin/sh", "-c", "head -c 140000 /dev/zero"}, 10); status.Code(err) != codes.OutOfRange {
		t.Fatalf("output limit=%v", err)
	}
	if err := vm.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	node.journal.sandboxes[id] = sandboxRecord{SandboxID: id, State: "stopped", Generation: 7, Revision: 12}
	if _, err := call(ctx, "stopped-key-001", []string{"/bin/true"}, 10); status.Code(err) != codes.Aborted {
		t.Fatalf("stopped fence=%v", err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	boot(13)
	if _, err := call(ctx, "retry-key-0001", argv, 10); status.Code(err) != codes.Aborted {
		t.Fatalf("stale revision after restart=%v", err)
	}
	input.Revision = 13
	restarted, err := call(ctx, "retry-key-0001", argv, 10)
	if err != nil || restarted.ExecutionID == first.ExecutionID {
		t.Fatalf("new boot did not fence old execution: %+v %v", restarted, err)
	}
	if err := vm.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	delete(node.live, id)
	delete(node.journal.sandboxes, id)
	if _, err := call(ctx, "deleted-key-001", argv, 10); status.Code(err) != codes.NotFound {
		t.Fatalf("deleted fence=%v", err)
	}
	t.Log("real CHV: stdout/stderr, nonzero exit, retry/conflict, timeout, cancellation, output cap, stopped/restarted/deleted admission fencing passed")
}
