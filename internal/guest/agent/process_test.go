package agent

import (
	"strings"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGuestProcessStartOutputAndDeduplication(t *testing.T) {
	identity := &guestv1.RuntimeIdentity{
		SandboxId:           "sandbox-process-test",
		OwnershipGeneration: 3,
		BootId:              "boot-process-test",
		GuestEpoch:          "epoch-process-test",
	}
	server := NewServer(Identity{
		SandboxID:           identity.GetSandboxId(),
		OwnershipGeneration: identity.GetOwnershipGeneration(),
		BootID:              identity.GetBootId(),
		GuestEpoch:          identity.GetGuestEpoch(),
	})
	server.workspace = t.TempDir()
	request := &guestv1.StartProcessRequest{
		Identity:  identity,
		RequestId: "process-output-1",
		Command:   &guestv1.Command{Argv: []string{"/bin/sh", "-c", "printf stdout; printf stderr >&2"}},
	}
	started, err := server.StartProcess(t.Context(), request)
	if err != nil {
		t.Fatalf("start process: %v", err)
	}
	retried, err := server.StartProcess(t.Context(), request)
	if err != nil {
		t.Fatalf("retry process start: %v", err)
	}
	if retried.GetRef().GetExecutionId() != started.GetRef().GetExecutionId() {
		t.Fatalf("retry started a second process: first=%s retry=%s", started.GetRef().GetExecutionId(), retried.GetRef().GetExecutionId())
	}

	conflict := *request
	conflict.Command = &guestv1.Command{Argv: []string{"/bin/true"}}
	if _, err := server.StartProcess(t.Context(), &conflict); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting request ID error = %v, want AlreadyExists", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var result *guestv1.ProcessInfo
	for time.Now().Before(deadline) {
		result, err = server.GetProcess(t.Context(), started.GetRef())
		if err != nil {
			t.Fatalf("get process: %v", err)
		}
		if result.GetState() == guestv1.ProcessState_PROCESS_STATE_EXITED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result.GetState() != guestv1.ProcessState_PROCESS_STATE_EXITED {
		t.Fatal("process did not exit before deadline")
	}
	if result.GetResult().GetExitCode() != 0 {
		t.Fatalf("process exit result = %+v", result.GetResult())
	}
	server.processMu.Lock()
	process := server.processes[started.GetRef().GetExecutionId()]
	server.processMu.Unlock()
	process.mu.Lock()
	defer process.mu.Unlock()
	var stdout, stderr strings.Builder
	for _, event := range process.events {
		if output := event.GetOutput(); output != nil {
			switch output.GetChannel() {
			case guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT:
				stdout.Write(output.GetData())
			case guestv1.OutputChannel_OUTPUT_CHANNEL_STDERR:
				stderr.Write(output.GetData())
			}
		}
	}
	if stdout.String() != "stdout" || stderr.String() != "stderr" {
		t.Fatalf("process output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestGuestProcessSignalTerminatesProcessGroup(t *testing.T) {
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox-signal-test", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	server := NewServer(Identity{SandboxID: identity.GetSandboxId(), OwnershipGeneration: 1, BootID: "boot", GuestEpoch: "epoch"})
	server.workspace = t.TempDir()
	started, err := server.StartProcess(t.Context(), &guestv1.StartProcessRequest{
		Identity:  identity,
		RequestId: "process-signal-1",
		Command:   &guestv1.Command{Argv: []string{"/bin/sleep", "30"}},
	})
	if err != nil {
		t.Fatalf("start process: %v", err)
	}
	if _, err := server.SignalProcess(t.Context(), &guestv1.SignalProcessRequest{Ref: started.GetRef(), Signal: guestv1.ProcessSignal_PROCESS_SIGNAL_TERMINATE}); err != nil {
		t.Fatalf("terminate process: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := server.GetProcess(t.Context(), started.GetRef())
		if err != nil {
			t.Fatalf("get process: %v", err)
		}
		if info.GetState() == guestv1.ProcessState_PROCESS_STATE_EXITED {
			if info.GetResult().GetSignal() == 0 {
				t.Fatalf("signal exit was not recorded: %+v", info.GetResult())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("process did not terminate before deadline")
}
