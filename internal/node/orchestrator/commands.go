package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const commandRunMethod = "/isodo.node.v1.CommandRunner/RunCommand"

// The Node mTLS gRPC server enforces a 1 MiB response limit. Text output can
// expand up to sixfold when serialized as JSON, so cap raw output at 128 KiB.
const commandOutputLimit = 128 << 10

// commandRunnerRPC is intentionally a *narrow* service. Its only operation is
// RunCommand: callers cannot dial arbitrary Guest RPC methods, files or ports.
// Until typed generated messages are introduced, requests and responses use
// a protobuf BytesValue containing a bounded JSON runtimecommands envelope.
type commandRunnerRPC interface {
	RunCommand(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}

var commandRunnerServiceDesc = grpc.ServiceDesc{
	ServiceName: "isodo.node.v1.CommandRunner",
	HandlerType: (*commandRunnerRPC)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "RunCommand",
		Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := new(wrapperspb.BytesValue)
			if err := decode(in); err != nil {
				return nil, err
			}
			if interceptor == nil {
				return srv.(commandRunnerRPC).RunCommand(ctx, in)
			}
			info := &grpc.UnaryServerInfo{Server: srv, FullMethod: commandRunMethod}
			return interceptor(ctx, in, info, func(ctx context.Context, request any) (any, error) {
				return srv.(commandRunnerRPC).RunCommand(ctx, request.(*wrapperspb.BytesValue))
			})
		},
	}},
}

func RegisterCommandRunner(server *grpc.Server, node *CHVServer) {
	server.RegisterService(&commandRunnerServiceDesc, node)
}

// RunCommand validates the Node-owned VM and delegates ONLY to its private
// Guest Process API. The host never executes caller-supplied commands.
func (s *CHVServer) RunCommand(ctx context.Context, request *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if request == nil || len(request.GetValue()) == 0 || len(request.GetValue()) > 64<<10 {
		return nil, status.Error(codes.InvalidArgument, "invalid command envelope")
	}
	var input runtimecommands.Request
	if err := json.Unmarshal(request.GetValue(), &input); err != nil {
		return nil, status.Error(codes.InvalidArgument, "malformed command envelope")
	}
	if !canonicalUUID(input.SandboxID) || input.Generation == 0 || input.Revision == 0 ||
		!validCommandKey(input.Key) || len(input.Command.Argv) == 0 || len(input.Command.Argv) > 256 ||
		input.Command.Argv[0] == "" || input.Command.TimeoutSeconds < 1 || input.Command.TimeoutSeconds > 300 {
		return nil, status.Error(codes.InvalidArgument, "invalid command target or arguments")
	}
	s.mu.Lock()
	record, ok := s.journal.getSandbox(input.SandboxID)
	instance := s.live[input.SandboxID]
	busy := s.busy[input.SandboxID]
	s.mu.Unlock()
	if !ok || instance == nil {
		return nil, status.Error(codes.NotFound, "sandbox runtime not found")
	}
	if record.State != "running" || busy || record.Generation != input.Generation || record.Revision != input.Revision {
		return nil, status.Error(codes.Aborted, "sandbox runtime changed or is not running")
	}
	identity := instance.Identity()
	if identity == nil || identity.GetSandboxId() != input.SandboxID ||
		identity.GetOwnershipGeneration() != record.Generation ||
		identity.GetBootId() != record.BootID || identity.GetGuestEpoch() != record.GuestEpoch ||
		record.BootID == "" || record.GuestEpoch == "" {
		return nil, status.Error(codes.Aborted, "sandbox runtime identity changed")
	}
	if running, err := instance.Running(ctx); err != nil || !running {
		return nil, status.Error(codes.FailedPrecondition, "sandbox VM is not running")
	}
	socket := instance.VsockSocket()
	if socket == "" {
		return nil, status.Error(codes.FailedPrecondition, "sandbox has no Guest transport")
	}
	// Node owns the Vsock and picks the socket from its own journal/live map.
	connection, err := guesttransport.NewVsockClientConn(socket)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "Guest management transport unavailable")
	}
	defer connection.Close()
	guest := guestv1.NewGuestServiceClient(connection)
	// The same caller-provided key + Sandbox runtime maps to a stable Guest
	// request_id. Reusing the key with a different argv/env/timeout will be
	// rejected by the Guest's existing idempotency conflict check.
	requestID := guestCommandRequestID(input.SandboxID, input.Generation, record.BootID, record.GuestEpoch, input.Key)
	timeout := uint32(input.Command.TimeoutSeconds)
	start := &guestv1.StartProcessRequest{
		Identity:              identity,
		RequestId:             requestID,
		Command:               &guestv1.Command{Argv: input.Command.Argv},
		Cwd:                   &input.Command.Cwd,
		Env:                   input.Command.Env,
		CommandTimeoutSeconds: &timeout,
	}
	process, err := guest.StartProcess(ctx, start)
	if err != nil {
		return nil, err
	}
	if process.GetRef() == nil || process.GetRef().GetExecutionId() == "" {
		return nil, status.Error(codes.Internal, "Guest returned an invalid process reference")
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		// Cancellation, output gaps and transport failures must not allow a
		// known process to remain running. Use an independent cleanup deadline.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, _ = guest.SignalProcess(cleanup, &guestv1.SignalProcessRequest{
			Ref: process.GetRef(), Signal: guestv1.ProcessSignal_PROCESS_SIGNAL_KILL,
		})
	}()
	stream, err := guest.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		return nil, err
	}
	result := runtimecommands.Result{ExecutionID: process.GetRef().GetExecutionId()}
	var stdout, stderr bytes.Buffer
	for {
		event, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, status.Error(codes.DataLoss, "Guest command ended without exit event")
			}
			return nil, err
		}
		if event.GetGap() != nil {
			return nil, status.Error(codes.DataLoss, "Guest command output was lost")
		}
		if output := event.GetOutput(); output != nil {
			if stdout.Len()+stderr.Len()+len(output.GetData()) > commandOutputLimit {
				return nil, status.Error(codes.OutOfRange, "Guest command exceeded 128 KiB output limit")
			}
			switch output.GetChannel() {
			case guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT:
				_, _ = stdout.Write(output.GetData())
			case guestv1.OutputChannel_OUTPUT_CHANNEL_STDERR:
				_, _ = stderr.Write(output.GetData())
			default:
				return nil, status.Error(codes.DataLoss, "Unexpected Guest output channel")
			}
		}
		if exit := event.GetExit(); exit != nil {
			if exit.GetOutputIncomplete() {
				return nil, status.Error(codes.DataLoss, "Guest reported incomplete output")
			}
			result.Stdout, result.Stderr = stdout.String(), stderr.String()
			result.ExitCode = exit.ExitCode
			result.Reason = exit.GetReason().String()
			finished = true
			encoded, err := json.Marshal(result)
			if err != nil {
				return nil, status.Error(codes.Internal, "cannot encode Guest result")
			}
			return wrapperspb.Bytes(encoded), nil
		}
	}
}

func validCommandKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, char := range []byte(value) {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

// guestCommandRequestID is stable across transport retries in the same VM run.
// Stop/Start or restore changes bootID/epoch, so the old key cannot refer to
// an execution in a new runtime.
func guestCommandRequestID(sandboxID string, generation uint64, bootID, epoch, key string) string {
	sum := sha256.Sum256([]byte(sandboxID + ":" + strconv.FormatUint(generation, 10) + ":" + bootID + ":" + epoch + ":" + key))
	return hex.EncodeToString(sum[:])
}
