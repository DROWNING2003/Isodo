package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const maxCommandOutputBytes = 1 << 20

// RunCommand executes only inside the verified Guest of an assigned, running
// Sandbox. Control→Node is mTLS and the Node→Guest hop is a private Vsock stream.
// No Host shell or Node-local executable is involved.
func (c *Client) RunCommand(ctx context.Context, target *nodev1.SandboxTarget, input sandbox.CommandRequest) (sandbox.CommandResult, error) {
	if target == nil || target.GetSandboxId() == "" || target.GetOwnershipGeneration() == 0 {
		return sandbox.CommandResult{}, status.Error(codes.InvalidArgument, "Sandbox runtime target is required")
	}
	inspection, err := c.InspectSandbox(ctx, &nodev1.InspectSandboxRequest{
		SandboxId: target.GetSandboxId(), OwnershipGeneration: target.GetOwnershipGeneration(),
	})
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	if inspection.GetSandboxId() != target.GetSandboxId() || inspection.GetOwnershipGeneration() != target.GetOwnershipGeneration() ||
		inspection.GetObservedState() != "running" || inspection.GetBootId() == "" || inspection.GetGuestEpoch() == "" {
		return sandbox.CommandResult{}, status.Error(codes.FailedPrecondition, "Sandbox runtime is not running with a valid identity")
	}
	conn, err := grpc.NewClient("passthrough:///isodo-guest", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(dialCtx context.Context, _ string) (net.Conn, error) {
			return c.RelayPort(dialCtx, target.GetSandboxId(), target.GetOwnershipGeneration(), 1024, "guest-rpc")
		}))
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	defer conn.Close()
	client := guestv1.NewGuestServiceClient(conn)
	identity := &guestv1.RuntimeIdentity{
		SandboxId: target.GetSandboxId(), OwnershipGeneration: target.GetOwnershipGeneration(),
		BootId: inspection.GetBootId(), GuestEpoch: inspection.GetGuestEpoch(),
	}
	timeout := uint32(input.TimeoutSeconds)
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity,
		RequestId: uuid.NewString(),
		Command: &guestv1.Command{Argv: input.Argv},
		Cwd: &input.Cwd,
		Env: input.Env,
		CommandTimeoutSeconds: &timeout,
	})
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	if process.GetRef() == nil {
		return sandbox.CommandResult{}, status.Error(codes.Internal, "Guest returned no process reference")
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		// Disconnects and truncated output must not leave a runaway process.
		killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, _ = client.SignalProcess(killCtx, &guestv1.SignalProcessRequest{
			Ref: process.GetRef(), Signal: guestv1.ProcessSignal_PROCESS_SIGNAL_KILL,
		})
	}()
	stream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	result := sandbox.CommandResult{ExecutionID: process.GetRef().GetExecutionId()}
	var stdout, stderr []byte
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return sandbox.CommandResult{}, status.Error(codes.DataLoss, "Guest command ended without an exit event")
			}
			return sandbox.CommandResult{}, recvErr
		}
		if event.GetGap() != nil {
			return sandbox.CommandResult{}, status.Error(codes.DataLoss, "Guest command output was lost")
		}
		if output := event.GetOutput(); output != nil {
			data := output.GetData()
			if len(stdout)+len(stderr)+len(data) > maxCommandOutputBytes {
				return sandbox.CommandResult{}, status.Error(codes.ResourceExhausted, "Guest command output exceeded 1 MiB")
			}
			switch output.GetChannel() {
			case guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT:
				stdout = append(stdout, data...)
			case guestv1.OutputChannel_OUTPUT_CHANNEL_STDERR:
				stderr = append(stderr, data...)
			default:
				return sandbox.CommandResult{}, status.Error(codes.DataLoss, "Unexpected Guest output channel")
			}
		}
		if exit := event.GetExit(); exit != nil {
			if exit.GetOutputIncomplete() {
				return sandbox.CommandResult{}, status.Error(codes.DataLoss, "Guest reported incomplete output")
			}
			result.Stdout = string(stdout)
			result.Stderr = string(stderr)
			result.ExitCode = exit.ExitCode
			result.Reason = exit.GetReason().String()
			result.OutputIncomplete = false
			completed = true
			return result, nil
		}
	}
}
