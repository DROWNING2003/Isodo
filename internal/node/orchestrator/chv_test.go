package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fetchFunc func(context.Context, string, string) (string, error)

func (f fetchFunc) Fetch(c context.Context, p, d string) (string, error) { return f(c, p, d) }

type fakeCHV struct {
	identity  *guestv1.RuntimeIdentity
	running   atomic.Bool
	directory string
}

func (f *fakeCHV) Identity() *guestv1.RuntimeIdentity {
	return proto.Clone(f.identity).(*guestv1.RuntimeIdentity)
}
func (f *fakeCHV) VsockSocket() string                   { return "" }
func (f *fakeCHV) Running(context.Context) (bool, error) { return f.running.Load(), nil }
func (f *fakeCHV) Stop(context.Context) error            { f.running.Store(false); return nil }
func (f *fakeCHV) Close()                                { f.running.Store(false) }
func (f *fakeCHV) Delete() error                         { f.Close(); return os.RemoveAll(f.directory) }
func testCHVStatus() NodeStatus {
	return NodeStatus{NodeID: "test-node", BootID: "host-boot", TotalVCPU: 2, AvailableVCPU: 2, TotalMemoryBytes: 512 << 20, AvailableMemoryBytes: 512 << 20}
}
func createCHVRequest() *nodev1.ApplyOperationRequest {
	digest := strings.Repeat("a", 64)
	return &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), SandboxId: uuid.NewString(), RequestHash: strings.Repeat("b", 64), OwnershipGeneration: 1, Revision: 1, Action: nodev1.OperationAction_OPERATION_ACTION_CREATE, TemplateId: uuid.NewString(), TemplateBuildId: uuid.NewString(), TemplateArtifact: &nodev1.TemplateArtifact{ManifestDigest: digest, Bucket: "templates", Prefix: "templates/sha256/" + digest}, Resources: &nodev1.ResourceSpec{Vcpu: 2, MemoryBytes: 512 << 20}, Network: &nodev1.NetworkSpec{EgressMode: nodev1.EgressMode_EGRESS_MODE_DENY}}
}
func waitCHV(t *testing.T, s *CHVServer, id string) *nodev1.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		result, err := s.GetOperation(ctx, &nodev1.GetOperationRequest{OperationId: id})
		if err != nil {
			t.Fatal(err)
		}
		if terminal(result.State) {
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation did not finish")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func TestCHVJournalFencingCapacityAndLifecycle(t *testing.T) {
	directory := t.TempDir()
	request := createCHVRequest()
	release := make(chan struct{})
	fetched := make(chan struct{})
	var fetchedOnce sync.Once
	var launches atomic.Int32
	config := CHVConfig{Directory: directory, Binary: "test", Bucket: "templates"}
	config.Fetcher = fetchFunc(func(ctx context.Context, _, _ string) (string, error) {
		data, err := os.ReadFile(filepath.Join(directory, "journal.json"))
		if err != nil || !strings.Contains(string(data), request.OperationId) {
			return "", errors.New("intent was not durable before fetch")
		}
		fetchedOnce.Do(func() { close(fetched) })
		select {
		case <-release:
			return "fixture", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	config.Launch = func(ctx, life context.Context, c templatebuilder.RuntimeConfig) (CHVRuntime, error) {
		launches.Add(1)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := life.Err(); err != nil {
			return nil, err
		}
		if err := os.Mkdir(c.Directory, 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(c.Directory, "workspace"), []byte("retained"), 0600); err != nil {
			return nil, err
		}
		identity := proto.Clone(c.Identity).(*guestv1.RuntimeIdentity)
		identity.GuestEpoch = "epoch"
		f := &fakeCHV{identity: identity, directory: c.Directory}
		f.running.Store(true)
		return f, nil
	}
	config.Restart = func(ctx, life context.Context, c templatebuilder.RuntimeConfig) (CHVRuntime, error) {
		if _, err := os.Stat(filepath.Join(c.Directory, "workspace")); err != nil {
			return nil, err
		}
		identity := proto.Clone(c.Identity).(*guestv1.RuntimeIdentity)
		identity.GuestEpoch = "epoch-2"
		f := &fakeCHV{identity: identity, directory: c.Directory}
		f.running.Store(true)
		return f, nil
	}
	server, err := NewCHVServer(testCHVStatus(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	createCtx, cancel := context.WithCancel(t.Context())
	accepted, err := server.ApplyOperation(createCtx, request)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if accepted.Operation.State != nodev1.OperationState_OPERATION_STATE_ACCEPTED {
		t.Fatal("operation was not accepted durably")
	}
	select {
	case <-fetched:
	case <-time.After(time.Second):
		t.Fatal("worker did not fetch")
	}
	repeated, err := server.ApplyOperation(t.Context(), request)
	if err != nil || !repeated.AlreadyApplied {
		t.Fatalf("replay: %+v %v", repeated, err)
	}
	modified := proto.Clone(request).(*nodev1.ApplyOperationRequest)
	modified.Resources.Vcpu = 1
	if _, err = server.ApplyOperation(t.Context(), modified); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("same hash with modified body: %v", err)
	}
	if _, err = server.ApplyOperation(t.Context(), createCHVRequest()); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("pending create did not reserve capacity: %v", err)
	}
	node, err := server.GetNodeStatus(t.Context(), new(emptypb.Empty))
	if err != nil || node.AvailableMemoryBytes != 0 || node.AvailableVcpu != 0 {
		t.Fatalf("capacity %+v %v", node, err)
	}
	close(release)
	result := waitCHV(t, server, request.OperationId)
	if result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED || launches.Load() != 1 {
		t.Fatalf("create result: %+v", result)
	}
	firstBoot := result.BootId
	if firstBoot == "" {
		t.Fatal("create did not record a boot ID")
	}
	inspection, err := server.InspectSandbox(t.Context(), &nodev1.InspectSandboxRequest{SandboxId: request.SandboxId, OwnershipGeneration: 1})
	if err != nil || inspection.ObservedState != "running" || inspection.GuestEpoch != "epoch" {
		t.Fatalf("inspection %+v %v", inspection, err)
	}
	stop := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), RequestHash: strings.Repeat("c", 64), SandboxId: request.SandboxId, OwnershipGeneration: 1, Revision: 1, Action: nodev1.OperationAction_OPERATION_ACTION_STOP}
	if _, err = server.ApplyOperation(t.Context(), stop); status.Code(err) != codes.Aborted {
		t.Fatalf("stale revision: %v", err)
	}
	stop.Revision = 2
	if _, err = server.ApplyOperation(t.Context(), stop); err != nil {
		t.Fatal(err)
	}
	if result = waitCHV(t, server, stop.OperationId); result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
		t.Fatal(result)
	}
	if _, err = os.Stat(filepath.Join(directory, "vms", request.SandboxId, "workspace")); err != nil {
		t.Fatalf("stop removed disk: %v", err)
	}
	start := &nodev1.ApplyOperationRequest{OperationId: uuid.NewString(), RequestHash: strings.Repeat("d", 64), SandboxId: request.SandboxId, OwnershipGeneration: 1, Revision: 3, Action: nodev1.OperationAction_OPERATION_ACTION_START}
	if _, err = server.ApplyOperation(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if result = waitCHV(t, server, start.OperationId); result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
		t.Fatal(result)
	}
	if result.BootId == "" || result.BootId == firstBoot {
		t.Fatalf("start reused boot ID: %+v", result)
	}
	inspection, err = server.InspectSandbox(t.Context(), &nodev1.InspectSandboxRequest{SandboxId: request.SandboxId, OwnershipGeneration: 1})
	if err != nil || inspection.ObservedState != "running" || inspection.GuestEpoch != "epoch-2" {
		t.Fatalf("start inspection %+v %v", inspection, err)
	}
	stop2 := proto.Clone(stop).(*nodev1.ApplyOperationRequest)
	stop2.OperationId = uuid.NewString()
	stop2.RequestHash = strings.Repeat("e", 64)
	stop2.Revision = 4
	if _, err = server.ApplyOperation(t.Context(), stop2); err != nil {
		t.Fatal(err)
	}
	if result = waitCHV(t, server, stop2.OperationId); result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
		t.Fatal(result)
	}
	deletion := proto.Clone(stop).(*nodev1.ApplyOperationRequest)
	deletion.OperationId = uuid.NewString()
	deletion.RequestHash = strings.Repeat("f", 64)
	deletion.Revision = 5
	deletion.Action = nodev1.OperationAction_OPERATION_ACTION_DELETE
	if _, err = server.ApplyOperation(t.Context(), deletion); err != nil {
		t.Fatal(err)
	}
	if result = waitCHV(t, server, deletion.OperationId); result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
		t.Fatal(result)
	}
	if _, err = os.Stat(filepath.Join(directory, "vms", request.SandboxId)); !os.IsNotExist(err) {
		t.Fatal("delete retained writable disk directory")
	}
	server.Close()
	reopened, err := NewCHVServer(testCHVStatus(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay, err := reopened.ApplyOperation(t.Context(), request)
	if err != nil || !replay.AlreadyApplied {
		t.Fatalf("restarted replay: %+v %v", replay, err)
	}
}

func TestCHVRejectsUnsafeArtifactAndNetwork(t *testing.T) {
	config := CHVConfig{Directory: t.TempDir(), Binary: "test", Bucket: "templates", Fetcher: fetchFunc(func(context.Context, string, string) (string, error) {
		t.Error("rejected request reached storage")
		return "", nil
	})}
	server, err := NewCHVServer(testCHVStatus(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	request := createCHVRequest()
	request.TemplateArtifact.Prefix = "../other"
	if _, err = server.ApplyOperation(t.Context(), request); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unsafe prefix: %v", err)
	}
	request = createCHVRequest()
	request.TemplateArtifact.Bucket = "other"
	if _, err = server.ApplyOperation(t.Context(), request); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("other bucket: %v", err)
	}
	request = createCHVRequest()
	request.Network.EgressMode = nodev1.EgressMode_EGRESS_MODE_ALLOW
	if _, err = server.ApplyOperation(t.Context(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unsupported egress: %v", err)
	}
}

func TestCHVStartupRefusesUnfinishedJournal(t *testing.T) {
	config := CHVConfig{Directory: t.TempDir(), Binary: "test", Bucket: "templates", Fetcher: fetchFunc(func(context.Context, string, string) (string, error) { return "", nil })}
	journal, err := openLimaJournal(filepath.Join(config.Directory, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	request := createCHVRequest()
	if err = journal.saveOperation(journalOperation{Request: request, Operation: &nodev1.Operation{OperationId: request.OperationId, State: nodev1.OperationState_OPERATION_STATE_RUNNING}}); err != nil {
		t.Fatal(err)
	}
	if server, err := NewCHVServer(testCHVStatus(), config); err == nil {
		server.Close()
		t.Fatal("claimed recovery without resolving previous VM")
	}
}

func TestCHVFailureDoesNotPublishReadyOrLeakDiagnostics(t *testing.T) {
	for _, wrongIdentity := range []bool{false, true} {
		t.Run(map[bool]string{false: "launch-failed", true: "wrong-identity"}[wrongIdentity], func(t *testing.T) {
			var returned *fakeCHV
			config := CHVConfig{Directory: t.TempDir(), Binary: "test", Bucket: "templates", Fetcher: fetchFunc(func(context.Context, string, string) (string, error) { return "fixture", nil })}
			config.Launch = func(context.Context, context.Context, templatebuilder.RuntimeConfig) (CHVRuntime, error) {
				if !wrongIdentity {
					return nil, errors.New("internal diagnostic: test-secret-marker")
				}
				returned = &fakeCHV{identity: &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: "wrong", GuestEpoch: "wrong"}}
				returned.running.Store(true)
				return returned, nil
			}
			server, err := NewCHVServer(testCHVStatus(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			request := createCHVRequest()
			if _, err = server.ApplyOperation(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			operation := waitCHV(t, server, request.OperationId)
			if operation.State != nodev1.OperationState_OPERATION_STATE_FAILED || operation.BootId != "" || strings.Contains(operation.Message, "test-secret-marker") {
				t.Fatalf("failure published Ready or raw diagnostic: %+v", operation)
			}
			if returned != nil && returned.running.Load() {
				t.Fatal("wrong-identity VM was left running")
			}
			capacity, err := server.GetNodeStatus(t.Context(), new(emptypb.Empty))
			if err != nil || capacity.AvailableVcpu != 2 {
				t.Fatalf("failed create retained capacity: %+v %v", capacity, err)
			}
		})
	}
}

func TestCHVStateDirectoryHasOneOwner(t *testing.T) {
	config := CHVConfig{Directory: t.TempDir(), Binary: "test", Bucket: "templates", Fetcher: fetchFunc(func(context.Context, string, string) (string, error) { return "", nil })}
	first, err := NewCHVServer(testCHVStatus(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := NewCHVServer(testCHVStatus(), config); err == nil {
		second.Close()
		t.Fatal("two Nodes share one journal and writable disk namespace")
	}
	first.Close()
	next, err := NewCHVServer(testCHVStatus(), config)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestCHVJournalOwnsOperationAndRequestSnapshots(t *testing.T) {
	journal, err := openLimaJournal(filepath.Join(t.TempDir(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	writer := &CHVServer{journal: journal}
	request := createCHVRequest()
	operation := &nodev1.Operation{OperationId: request.OperationId, State: nodev1.OperationState_OPERATION_STATE_ACCEPTED}
	entry := journalOperation{Request: request, Operation: operation}
	if err = writer.persistIntent(entry, sandboxRecord{SandboxID: request.SandboxId}); err != nil {
		t.Fatal(err)
	}
	operation.State = nodev1.OperationState_OPERATION_STATE_RUNNING
	request.TemplateId = "changed"
	stored, _ := journal.getOperation(operation.OperationId)
	if stored.Operation.State != nodev1.OperationState_OPERATION_STATE_ACCEPTED || stored.Request.TemplateId == "changed" {
		t.Fatal("intent journal retained caller-owned mutable protobufs")
	}
	if err = writer.persistOperation(entry); err != nil {
		t.Fatal(err)
	}
	operation.State = nodev1.OperationState_OPERATION_STATE_SUCCEEDED
	request.TemplateId = "changed-again"
	stored, _ = journal.getOperation(operation.OperationId)
	if stored.Operation.State != nodev1.OperationState_OPERATION_STATE_RUNNING || stored.Request.TemplateId != "changed" {
		t.Fatal("operation journal retained worker-owned mutable protobufs")
	}
}
