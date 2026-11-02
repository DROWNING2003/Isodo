package orchestrator

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type bootEgressGuest struct {
	guestv1.UnimplementedGuestServiceServer
	mu      sync.Mutex
	files   map[string]string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	fail    bool
	started *guestv1.StartProcessRequest
}

func (g *bootEgressGuest) WriteFile(stream guestv1.GuestService_WriteFileServer) error {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	if g.fail {
		return status.Error(codes.Unavailable, "fixture write failure")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	path := first.GetHeader().GetPath()
	var content strings.Builder
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		content.Write(frame.GetChunk().GetData())
	}
	g.mu.Lock()
	g.files[path] = content.String()
	g.mu.Unlock()
	return stream.SendAndClose(&guestv1.WriteFileResponse{})
}
func (g *bootEgressGuest) StartProcess(_ context.Context, r *guestv1.StartProcessRequest) (*guestv1.ProcessInfo, error) {
	g.mu.Lock()
	g.started = proto.Clone(r).(*guestv1.StartProcessRequest)
	g.mu.Unlock()
	// Initial Ready must prepare networking without requiring or restarting T3.
	if strings.Contains(strings.Join(r.Command.Argv, " "), "t3.pid") {
		return nil, status.Error(codes.FailedPrecondition, "API template has no T3")
	}
	return &guestv1.ProcessInfo{Ref: &guestv1.ProcessRef{ExecutionId: "fixture-bootstrap"}}, nil
}
func (g *bootEgressGuest) SubscribeProcess(_ *guestv1.SubscribeProcessRequest, s guestv1.GuestService_SubscribeProcessServer) error {
	return s.Send(&guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Exit{Exit: &guestv1.ProcessExit{ExitCode: new(int32(0)), Reason: guestv1.ExitReason_EXIT_REASON_COMPLETED}}})
}
func TestCHVReadyWaitsForEgressWithoutWorkbench(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "failure"}[fail], func(t *testing.T) {
			guest := &bootEgressGuest{files: map[string]string{}, entered: make(chan struct{}), release: make(chan struct{}), fail: fail}
			socket := commandGuestRelay(t, guest)
			config := CHVConfig{Directory: t.TempDir(), Binary: "fixture", Bucket: "templates", Fetcher: fetchFunc(func(context.Context, string, string) (string, error) { return "fixture", nil })}
			config.Launch = func(_ context.Context, _ context.Context, c templatebuilder.RuntimeConfig) (CHVRuntime, error) {
				identity := proto.Clone(c.Identity).(*guestv1.RuntimeIdentity)
				identity.GuestEpoch = "fixture-epoch-" + identity.BootId
				runtime := &commandTestRuntime{fakeCHV: &fakeCHV{identity: identity}, socket: socket}
				runtime.running.Store(true)
				return runtime, nil
			}
			config.Restart = config.Launch
			node, err := NewCHVServer(testCHVStatus(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer node.Close()
			manager, err := injection.NewManager(injection.ManagerConfig{StateDir: t.TempDir(), HostGateway: "127.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			node.EnableInjections(manager)
			request := createCHVRequest()
			request.Network.EgressMode = nodev1.EgressMode_EGRESS_MODE_ALLOW
			if _, err = node.ApplyOperation(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			select {
			case <-guest.entered:
			case <-time.After(3 * time.Second):
				close(guest.release)
				t.Fatal("Create did not prepare egress before reporting Ready")
			}
			operation, err := node.GetOperation(t.Context(), &nodev1.GetOperationRequest{OperationId: request.OperationId})
			if err != nil || terminal(operation.State) {
				t.Fatal("reported Ready before egress completed", err)
			}
			close(guest.release)
			operation = waitCHV(t, node, request.OperationId)
			if fail {
				if operation.State != nodev1.OperationState_OPERATION_STATE_FAILED {
					t.Fatal("egress failure reported Ready")
				}
				return
			}
			if operation.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
				t.Fatal(operation)
			}
			guest.mu.Lock()
			env := guest.files["/workspace/.isodo/proxy.env"]
			guest.mu.Unlock()
			if !strings.Contains(env, "127.0.0.1:31080") || !strings.Contains(env, "ca-bundle.crt") {
				t.Fatal("Guest proxy environment missing")
			}
			node.mu.Lock()
			binding := node.egress[request.SandboxId]
			node.mu.Unlock()
			if binding == nil {
				t.Fatal("Ready closed the newly prepared egress bridge")
			}
			input := runtimecommands.Request{SandboxID: request.SandboxId, Generation: 1, Revision: 1, Key: "fresh-api-agent-123", Command: runtimecommands.Command{Argv: []string{"codex", "--version"}, Cwd: "/workspace", Env: map[string]string{"CODEX_HOME": "/workspace/.codex", "NO_PROXY": "caller-choice"}, TimeoutSeconds: 30}}
			if _, err = node.RunCommand(t.Context(), wrapperspb.Bytes(mustMarshalCommand(t, input))); err != nil {
				t.Fatal(err)
			}
			guest.mu.Lock()
			started := guest.started
			guest.mu.Unlock()
			if !strings.Contains(started.Env["HTTPS_PROXY"], "127.0.0.1:31080") || started.Env["SSL_CERT_FILE"] != "/workspace/.isodo/ca-bundle.crt" || started.Env["NO_PROXY"] != "caller-choice" || started.Env["CODEX_HOME"] != "/workspace/.codex" {
				t.Fatal("Commands did not inherit proxy defaults and explicit environment")
			}
			stop := &nodev1.ApplyOperationRequest{OperationId: request.TemplateId, SandboxId: request.SandboxId, RequestHash: strings.Repeat("c", 64), OwnershipGeneration: 1, Revision: 2, Action: nodev1.OperationAction_OPERATION_ACTION_STOP}
			if _, err = node.ApplyOperation(t.Context(), stop); err != nil {
				t.Fatal(err)
			}
			if result := waitCHV(t, node, stop.OperationId); result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
				t.Fatal(result)
			}
			node.mu.Lock()
			afterStop := node.egress[request.SandboxId]
			node.mu.Unlock()
			if afterStop != nil {
				t.Fatal("Stop retained egress")
			}
			start := proto.Clone(stop).(*nodev1.ApplyOperationRequest)
			start.OperationId = request.TemplateBuildId
			start.RequestHash = strings.Repeat("d", 64)
			start.Revision = 3
			start.Action = nodev1.OperationAction_OPERATION_ACTION_START
			if _, err = node.ApplyOperation(t.Context(), start); err != nil {
				t.Fatal(err)
			}
			if result := waitCHV(t, node, start.OperationId); result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
				t.Fatal(result)
			}
			node.mu.Lock()
			restarted := node.egress[request.SandboxId]
			node.mu.Unlock()
			if restarted == nil || proto.Equal(restarted.identity, binding.identity) {
				t.Fatal("Start did not prepare fresh boot egress")
			}

		})
	}
}

func TestEgressSetupIdentityAndLifecycleFence(t *testing.T) {
	expected := sandboxRecord{SandboxID: "sandbox", Generation: 2, Revision: 7, BootID: "boot", State: "creating", EgressAllowed: true}
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 2, BootId: "boot", GuestEpoch: "epoch"}
	if !egressSetupMatches(expected, expected, identity, true) {
		t.Fatal("current boot worker rejected")
	}
	changes := []func(*sandboxRecord){func(r *sandboxRecord) { r.Revision++ }, func(r *sandboxRecord) { r.Generation++ }, func(r *sandboxRecord) { r.BootID = "old" }, func(r *sandboxRecord) { r.State = "stopped" }, func(r *sandboxRecord) { r.EgressAllowed = false }, func(r *sandboxRecord) { r.GuestEpoch = "wrong" }}
	for _, change := range changes {
		current := expected
		change(&current)
		if egressSetupMatches(expected, current, identity, true) {
			t.Fatal("stale boot preparation accepted")
		}
	}
	if egressSetupMatches(expected, expected, identity, false) {
		t.Fatal("boot setup without owning lifecycle operation accepted")
	}
	expected.State = "running"
	expected.GuestEpoch = "epoch"
	if !egressSetupMatches(expected, expected, identity, false) || egressSetupMatches(expected, expected, identity, true) {
		t.Fatal("running/busy fence changed")
	}
}
