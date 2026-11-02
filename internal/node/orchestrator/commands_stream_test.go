package orchestrator

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type commandTestGuest struct {
	guestv1.UnimplementedGuestServiceServer
	subscribe func(*guestv1.SubscribeProcessRequest, guestv1.GuestService_SubscribeProcessServer) error
	signaled  chan struct{}
}

func (g *commandTestGuest) StartProcess(context.Context, *guestv1.StartProcessRequest) (*guestv1.ProcessInfo, error) {
	return &guestv1.ProcessInfo{Ref: &guestv1.ProcessRef{ExecutionId: "test-execution"}}, nil
}
func (g *commandTestGuest) SubscribeProcess(r *guestv1.SubscribeProcessRequest, s guestv1.GuestService_SubscribeProcessServer) error {
	return g.subscribe(r, s)
}
func (g *commandTestGuest) SignalProcess(ctx context.Context, r *guestv1.SignalProcessRequest) (*emptypb.Empty, error) {
	if ctx.Err() != nil || r.GetSignal() != guestv1.ProcessSignal_PROCESS_SIGNAL_KILL {
		return nil, status.Error(codes.InvalidArgument, "invalid cleanup")
	}
	select {
	case g.signaled <- struct{}{}:
	default:
	}
	return &emptypb.Empty{}, nil
}

type commandTestRuntime struct {
	*fakeCHV
	socket string
}

func (r *commandTestRuntime) VsockSocket() string { return r.socket }

// A private Unix handshake relay exercises the actual Node→Guest gRPC path.
// These component tests do not execute commands on the host.
func commandGuestRelay(t *testing.T, guest guestv1.GuestServiceServer) string {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(server, guest)
	go func() { _ = server.Serve(tcp) }()
	t.Cleanup(server.Stop)
	dir, err := os.MkdirTemp("/tmp", "icmd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "v")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil || line != "CONNECT 1024\n" {
					return
				}
				if _, err = io.WriteString(conn, "OK 1024\n"); err != nil {
					return
				}
				_ = conn.SetDeadline(time.Time{})
				peer, err := net.Dial("tcp", tcp.Addr().String())
				if err != nil {
					return
				}
				defer peer.Close()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(peer, conn); _ = peer.Close(); close(done) }()
				_, _ = io.Copy(conn, peer)
				_ = conn.Close()
				<-done
			}()
		}
	}()
	return socket
}
func commandNodeForSocket(socket string) (*CHVServer, runtimecommands.Request) {
	id := "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"
	identity := &guestv1.RuntimeIdentity{SandboxId: id, OwnershipGeneration: 7, BootId: "boot", GuestEpoch: "epoch"}
	instance := &commandTestRuntime{fakeCHV: &fakeCHV{identity: identity}, socket: socket}
	instance.running.Store(true)
	node := &CHVServer{journal: &limaJournal{sandboxes: map[string]sandboxRecord{id: {SandboxID: id, State: "running", Generation: 7, Revision: 11, BootID: "boot", GuestEpoch: "epoch"}}}, live: map[string]CHVRuntime{id: instance}, busy: map[string]bool{}}
	input := runtimecommands.Request{SandboxID: id, Generation: 7, Revision: 11, Key: "request-12345678", Command: runtimecommands.Command{Argv: []string{"/bin/true"}, TimeoutSeconds: 30}}
	return node, input
}
func commandOutput(channel guestv1.OutputChannel, data string) *guestv1.ProcessEvent {
	return &guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Output{Output: &guestv1.ProcessOutput{Channel: channel, Data: []byte(data)}}}
}
func commandExit(incomplete bool) *guestv1.ProcessEvent {
	return &guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Exit{Exit: &guestv1.ProcessExit{ExitCode: new(int32(7)), OutputIncomplete: incomplete, Reason: guestv1.ExitReason_EXIT_REASON_COMPLETED}}}
}
func TestCommandGuestStreamResultsAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []*guestv1.ProcessEvent
		want   codes.Code
		kill   bool
	}{
		{"nonzero exit preserves both outputs", []*guestv1.ProcessEvent{commandOutput(guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT, "out\n"), commandOutput(guestv1.OutputChannel_OUTPUT_CHANNEL_STDERR, "err\n"), commandExit(false)}, codes.OK, false},
		{"output gap", []*guestv1.ProcessEvent{{Event: &guestv1.ProcessEvent_Gap{Gap: &guestv1.OutputGap{FirstMissingSeq: 1, LastMissingSeq: 3}}}}, codes.DataLoss, true},
		{"incomplete output", []*guestv1.ProcessEvent{commandExit(true)}, codes.DataLoss, true},
		{"missing exit", nil, codes.DataLoss, true},
		{"output cap", []*guestv1.ProcessEvent{commandOutput(guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT, strings.Repeat("x", commandOutputLimit+1))}, codes.OutOfRange, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guest := &commandTestGuest{signaled: make(chan struct{}, 1)}
			guest.subscribe = func(_ *guestv1.SubscribeProcessRequest, s guestv1.GuestService_SubscribeProcessServer) error {
				for _, e := range tc.events {
					if err := s.Send(e); err != nil {
						return err
					}
				}
				return nil
			}
			node, input := commandNodeForSocket(commandGuestRelay(t, guest))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			response, err := node.RunCommand(ctx, wrapperspb.Bytes(mustMarshalCommand(t, input)))
			if status.Code(err) != tc.want {
				t.Fatalf("status=%v want %v err=%v", status.Code(err), tc.want, err)
			}
			if tc.want == codes.OK {
				var r runtimecommands.Result
				if err := json.Unmarshal(response.Value, &r); err != nil {
					t.Fatal(err)
				}
				if r.Stdout != "out\n" || r.Stderr != "err\n" || r.ExitCode == nil || *r.ExitCode != 7 {
					t.Fatalf("result=%+v", r)
				}
			}
			select {
			case <-guest.signaled:
				if !tc.kill {
					t.Fatal("completed process was killed")
				}
			default:
				if tc.kill {
					t.Fatal("failed stream did not kill process")
				}
			}
		})
	}
}
func TestCommandCanceledStreamUsesIndependentCleanup(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			subscribed := make(chan struct{})
			guest := &commandTestGuest{signaled: make(chan struct{}, 1)}
			guest.subscribe = func(_ *guestv1.SubscribeProcessRequest, s guestv1.GuestService_SubscribeProcessServer) error {
				close(subscribed)
				<-s.Context().Done()
				return s.Context().Err()
			}
			node, input := commandNodeForSocket(commandGuestRelay(t, guest))
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			if !deadline {
				go func() {
					select {
					case <-subscribed:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			_, err := node.RunCommand(ctx, wrapperspb.Bytes(mustMarshalCommand(t, input)))
			want := codes.Canceled
			if deadline {
				want = codes.DeadlineExceeded
			}
			if status.Code(err) != want {
				t.Fatalf("code=%v err=%v", status.Code(err), err)
			}
			select {
			case <-guest.signaled:
			default:
				t.Fatal("canceled request did not independently clean up Guest process")
			}
		})
	}
}
