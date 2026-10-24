package orchestrator

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ArtifactFetcher interface {
	Fetch(context.Context, string, string) (string, error)
}
type verifiedArtifactFetcher interface {
	FetchVerified(context.Context, string, string) (*templateartifact.Artifact, error)
}
type CHVRuntime interface {
	Identity() *guestv1.RuntimeIdentity
	Running(context.Context) (bool, error)
	Stop(context.Context) error
	Close()
	Delete() error
}
type RuntimeLauncher func(context.Context, context.Context, templatebuilder.RuntimeConfig) (CHVRuntime, error)
type CHVConfig struct {
	Directory, Binary, Bucket string
	CacheDirectory            string
	Fetcher                   ArtifactFetcher
	Launch                    RuntimeLauncher
}
type CHVServer struct {
	stateLock *os.File
	nodev1.UnimplementedNodeOrchestratorServer
	status    *StatusServer
	config    CHVConfig
	journal   *limaJournal
	mu        sync.Mutex
	closed    bool
	busy      map[string]bool
	live      map[string]CHVRuntime
	allocated map[string]*nodev1.ResourceSpec
	ctx       context.Context
	cancel    context.CancelFunc
	life      context.Context
	endLife   context.CancelFunc
	workers   sync.WaitGroup
}

var sha256Text = regexp.MustCompile(`^[0-9a-f]{64}$`)

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func NewCHVServer(nodeStatus NodeStatus, config CHVConfig) (*CHVServer, error) {
	if config.Directory == "" || config.Binary == "" || config.Bucket == "" || config.Fetcher == nil {
		return nil, errors.New("CHV directory, binary, artifact bucket and fetcher are required")
	}
	var err error
	config.Directory, err = filepath.Abs(config.Directory)
	if err != nil {
		return nil, err
	}
	if config.CacheDirectory == "" {
		config.CacheDirectory = filepath.Join(config.Directory, "cache")
	}
	if err = os.MkdirAll(filepath.Join(config.Directory, "vms"), 0700); err != nil {
		return nil, err
	}
	stateLock, err := lockCHVDirectory(filepath.Join(config.Directory, "node.lock"))
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			stateLock.Close()
		}
	}()
	journal, err := openLimaJournal(filepath.Join(config.Directory, "journal.json"))
	if err != nil {
		return nil, err
	}
	// Do not claim recovery or launch replacements while a previous VMM may exist.
	// Process/cgroup reconciliation must settle these records before reopening.
	for _, entry := range journal.allOperations() {
		if !terminal(entry.Operation.State) {
			return nil, errors.New("CHV journal has an unfinished operation; runtime reconciliation is required")
		}
	}
	for _, record := range journal.sandboxes {
		if record.State == "running" || record.State == "creating" {
			return nil, errors.New("CHV journal has an unresolved runtime; process reconciliation is required")
		}
	}
	ss, err := NewStatusServer(nodeStatus)
	if err != nil {
		return nil, err
	}
	if config.Launch == nil {
		config.Launch = func(ctx, life context.Context, c templatebuilder.RuntimeConfig) (CHVRuntime, error) {
			return templatebuilder.StartRuntime(ctx, life, c)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	life, end := context.WithCancel(context.Background())
	retained = true
	return &CHVServer{stateLock: stateLock, status: ss, config: config, journal: journal, busy: make(map[string]bool), live: make(map[string]CHVRuntime), allocated: make(map[string]*nodev1.ResourceSpec), ctx: ctx, cancel: cancel, life: life, endLife: end}, nil
}

func (s *CHVServer) validate(request *nodev1.ApplyOperationRequest) error {
	if request == nil || !canonicalUUID(request.OperationId) || !canonicalUUID(request.SandboxId) || !sha256Text.MatchString(request.RequestHash) || request.OwnershipGeneration == 0 || request.Revision == 0 {
		return status.Error(codes.InvalidArgument, "canonical IDs, SHA-256 request hash, generation and revision are required")
	}
	switch request.Action {
	case nodev1.OperationAction_OPERATION_ACTION_CREATE:
		a := request.TemplateArtifact
		r := request.Resources
		if !canonicalUUID(request.TemplateId) || !canonicalUUID(request.TemplateBuildId) || a == nil || !sha256Text.MatchString(a.ManifestDigest) || a.Bucket != s.config.Bucket || a.Prefix != "templates/sha256/"+a.ManifestDigest {
			return status.Error(codes.InvalidArgument, "a pinned build and configured immutable artifact reference are required")
		}
		if r == nil || r.Vcpu == 0 || r.Vcpu > 64 || r.MemoryBytes < 128<<20 || r.MemoryBytes > 4<<30 {
			return status.Error(codes.InvalidArgument, "explicit supported CPU and memory resources are required")
		}
		if request.Network != nil && (request.Network.EgressMode != nodev1.EgressMode_EGRESS_MODE_DENY || len(request.Network.AllowedCidrs) > 0) {
			return status.Error(codes.FailedPrecondition, "this CHV backend currently supports egress deny only")
		}
	case nodev1.OperationAction_OPERATION_ACTION_STOP, nodev1.OperationAction_OPERATION_ACTION_DELETE:
		if request.TemplateArtifact != nil || request.TemplateId != "" || request.TemplateBuildId != "" || request.Resources != nil || request.Network != nil {
			return status.Error(codes.InvalidArgument, "stop/delete must not replace template or runtime configuration")
		}
	default:
		return status.Error(codes.Unimplemented, "this CHV backend supports create, stop and delete; disk-preserving start is pending")
	}
	return nil
}

func (s *CHVServer) ApplyOperation(ctx context.Context, request *nodev1.ApplyOperationRequest) (*nodev1.ApplyOperationResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.validate(request); err != nil {
		return nil, err
	}
	request = proto.Clone(request).(*nodev1.ApplyOperationRequest)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, status.Error(codes.Unavailable, "Node is shutting down")
	}
	if existing, ok := s.journal.getOperation(request.OperationId); ok {
		if !proto.Equal(existing.Request, request) {
			return nil, status.Error(codes.AlreadyExists, "operation ID is bound to another request")
		}
		return &nodev1.ApplyOperationResponse{Operation: existing.Operation, AlreadyApplied: true}, nil
	}
	record, exists := s.journal.getSandbox(request.SandboxId)
	if s.busy[request.SandboxId] {
		return nil, status.Error(codes.Aborted, "another operation owns this Sandbox")
	}
	if exists {
		if request.OwnershipGeneration < record.Generation || (request.OwnershipGeneration == record.Generation && request.Revision <= record.Revision) {
			return nil, status.Error(codes.Aborted, "stale Sandbox generation or revision")
		}
		if request.OwnershipGeneration > record.Generation && (record.State != "deleted" || request.Action != nodev1.OperationAction_OPERATION_ACTION_CREATE) {
			return nil, status.Error(codes.FailedPrecondition, "new ownership requires previous generation deletion")
		}
		if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE && record.State != "deleted" && record.State != "failed" {
			return nil, status.Error(codes.FailedPrecondition, "Sandbox already exists")
		}
	} else if request.Action != nodev1.OperationAction_OPERATION_ACTION_CREATE && request.Action != nodev1.OperationAction_OPERATION_ACTION_DELETE {
		return nil, status.Error(codes.NotFound, "Sandbox is not registered on this Node")
	}
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE {
		if s.status.status.Draining {
			return nil, status.Error(codes.Unavailable, "Node is draining")
		}
		cpus, memory := s.availableLocked()
		if request.Resources.Vcpu > cpus || request.Resources.MemoryBytes > memory {
			return nil, status.Error(codes.ResourceExhausted, "Node capacity is exhausted")
		}
	}
	operation := &nodev1.Operation{OperationId: request.OperationId, RequestHash: request.RequestHash, SandboxId: request.SandboxId, OwnershipGeneration: request.OwnershipGeneration, Revision: request.Revision, Action: request.Action, State: nodev1.OperationState_OPERATION_STATE_ACCEPTED, Stage: nodev1.OperationStage_OPERATION_STAGE_INTENT, CreatedAt: timestamppb.Now()}
	record.SandboxID = request.SandboxId
	record.Generation = request.OwnershipGeneration
	record.Revision = request.Revision
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE {
		record.State = "creating"
		record.TemplateID = request.TemplateId
		record.TemplateBuildID = request.TemplateBuildId
		record.BootID = uuid.NewString()
		record.GuestEpoch = ""
	}
	entry := journalOperation{Operation: operation, Request: request}
	if err := s.persistIntent(entry, record); err != nil {
		return nil, status.Error(codes.Internal, "could not persist operation intent")
	}
	s.busy[request.SandboxId] = true
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE {
		s.allocated[request.SandboxId] = proto.Clone(request.Resources).(*nodev1.ResourceSpec)
	}
	saved, _ := s.journal.getOperation(request.OperationId)
	response := &nodev1.ApplyOperationResponse{Operation: saved.Operation}
	s.workers.Go(func() { s.execute(entry, record) })
	return response, nil
}
func (s *CHVServer) availableLocked() (uint32, uint64) {
	cpus, memory := s.status.status.AvailableVCPU, s.status.status.AvailableMemoryBytes
	for _, r := range s.allocated {
		cpus -= min(cpus, r.Vcpu)
		memory -= min(memory, r.MemoryBytes)
	}
	return cpus, memory
}
func (s *CHVServer) execute(entry journalOperation, record sandboxRecord) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	entry.Operation.State = nodev1.OperationState_OPERATION_STATE_RUNNING
	entry.Operation.Stage = stageForAction(entry.Operation.Action)
	if entry.Operation.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE {
		entry.Operation.Stage = nodev1.OperationStage_OPERATION_STAGE_MATERIALIZE
	}
	if err := s.persistOperation(entry); err != nil {
		slog.Error("persist CHV operation stage", "error", err)
		return
	}
	var instance CHVRuntime
	var err error
	switch entry.Operation.Action {
	case nodev1.OperationAction_OPERATION_ACTION_CREATE:
		var artifact string
		var verified *templateartifact.Artifact
		if fetcher, ok := s.config.Fetcher.(verifiedArtifactFetcher); ok {
			verified, err = fetcher.FetchVerified(ctx, s.config.CacheDirectory, entry.Request.TemplateArtifact.ManifestDigest)
			if err == nil {
				artifact = verified.Directory()
			}
		} else {
			artifact, err = s.config.Fetcher.Fetch(ctx, s.config.CacheDirectory, entry.Request.TemplateArtifact.ManifestDigest)
		}
		if err == nil {
			entry.Operation.Stage = nodev1.OperationStage_OPERATION_STAGE_LAUNCH
			err = s.persistOperation(entry)
		}
		if err == nil {
			instance, err = s.config.Launch(ctx, s.life, templatebuilder.RuntimeConfig{Verified: verified, VMMBinary: s.config.Binary, ArtifactDirectory: artifact, ManifestDigest: entry.Request.TemplateArtifact.ManifestDigest, Directory: filepath.Join(s.config.Directory, "vms", record.SandboxID), Identity: &guestv1.RuntimeIdentity{SandboxId: record.SandboxID, OwnershipGeneration: record.Generation, BootId: record.BootID}, CPUs: entry.Request.Resources.Vcpu, MemoryBytes: entry.Request.Resources.MemoryBytes, DiskBytes: entry.Request.Resources.DiskBytes})
		}
		if err == nil {
			if instance == nil {
				err = errors.New("runtime launcher returned no VM")
			} else {
				identity := instance.Identity()
				if identity == nil || identity.SandboxId != record.SandboxID || identity.OwnershipGeneration != record.Generation || identity.BootId != record.BootID || identity.GuestEpoch == "" {
					err = errors.New("runtime returned an incompatible identity")
				}
			}
		}
	case nodev1.OperationAction_OPERATION_ACTION_STOP, nodev1.OperationAction_OPERATION_ACTION_DELETE:
		s.mu.Lock()
		instance = s.live[record.SandboxID]
		s.mu.Unlock()
		if instance != nil {
			if entry.Operation.Action == nodev1.OperationAction_OPERATION_ACTION_STOP {
				err = instance.Stop(ctx)
			} else {
				err = instance.Delete()
			}
		} else if entry.Operation.Action == nodev1.OperationAction_OPERATION_ACTION_DELETE {
			err = os.RemoveAll(filepath.Join(s.config.Directory, "vms", record.SandboxID))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		slog.Error("CHV operation failed", "operation_id", entry.Operation.OperationId, "error", err)
		entry.Operation.State = nodev1.OperationState_OPERATION_STATE_FAILED
		entry.Operation.ErrorCode = "CHV_OPERATION_FAILED"
		entry.Operation.Message = "Sandbox runtime operation failed."
		record.State = "failed"
		if instance != nil {
			instance.Close()
		}
		delete(s.live, record.SandboxID)
		delete(s.allocated, record.SandboxID)
	} else {
		entry.Operation.State = nodev1.OperationState_OPERATION_STATE_SUCCEEDED
		switch entry.Operation.Action {
		case nodev1.OperationAction_OPERATION_ACTION_CREATE:
			identity := instance.Identity()
			record.State = "running"
			record.BootID = identity.BootId
			record.GuestEpoch = identity.GuestEpoch
			entry.Operation.BootId = identity.BootId
			entry.Operation.Stage = nodev1.OperationStage_OPERATION_STAGE_GUEST_READY
			s.live[record.SandboxID] = instance
		case nodev1.OperationAction_OPERATION_ACTION_STOP:
			record.State = "stopped"
			record.BootID = ""
			record.GuestEpoch = ""
			delete(s.live, record.SandboxID)
			delete(s.allocated, record.SandboxID)
		case nodev1.OperationAction_OPERATION_ACTION_DELETE:
			record.State = "deleted"
			record.BootID = ""
			record.GuestEpoch = ""
			delete(s.live, record.SandboxID)
			delete(s.allocated, record.SandboxID)
		}
	}
	if saveErr := s.journal.saveSandbox(record); saveErr != nil {
		if instance != nil {
			instance.Close()
		}
		slog.Error("persist CHV Sandbox result", "error", saveErr)
		return
	}
	if saveErr := s.persistOperation(entry); saveErr != nil {
		if instance != nil {
			instance.Close()
		}
		slog.Error("persist CHV operation result", "error", saveErr)
		return
	}
	delete(s.busy, record.SandboxID)
}

func (s *CHVServer) GetOperation(_ context.Context, request *nodev1.GetOperationRequest) (*nodev1.Operation, error) {
	if request == nil || !canonicalUUID(request.OperationId) {
		return nil, status.Error(codes.InvalidArgument, "operation ID is required")
	}
	entry, ok := s.journal.getOperation(request.OperationId)
	if !ok {
		return nil, status.Error(codes.NotFound, "operation not found")
	}
	if request.RequestHash != "" && request.RequestHash != entry.Operation.RequestHash {
		return nil, status.Error(codes.FailedPrecondition, "operation hash mismatch")
	}
	return entry.Operation, nil
}
func (s *CHVServer) GetNodeStatus(ctx context.Context, e *emptypb.Empty) (*nodev1.NodeStatus, error) {
	result, err := s.status.GetNodeStatus(ctx, e)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result.AvailableVcpu, result.AvailableMemoryBytes = s.availableLocked()
	result.Draining = result.Draining || s.closed
	result.Capabilities = append(result.Capabilities, "sandbox.provider.chv.v1", "sandbox.create.v1", "sandbox.stop.v1", "sandbox.delete.v1", "sandbox.egress.deny.v1")
	return result, nil
}
func (s *CHVServer) InspectSandbox(ctx context.Context, request *nodev1.InspectSandboxRequest) (*nodev1.SandboxInspection, error) {
	if request == nil || !canonicalUUID(request.SandboxId) || request.OwnershipGeneration == 0 {
		return nil, status.Error(codes.InvalidArgument, "Sandbox ID and generation are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.journal.getSandbox(request.SandboxId)
	if !ok {
		return nil, status.Error(codes.NotFound, "Sandbox not found")
	}
	if record.Generation != request.OwnershipGeneration {
		return nil, status.Error(codes.Aborted, "ownership generation mismatch")
	}
	observed := record.State
	errorCode := ""
	if observed == "running" {
		instance := s.live[record.SandboxID]
		if instance == nil {
			observed = "failed"
			errorCode = "RUNTIME_UNAVAILABLE"
		} else if running, err := instance.Running(ctx); err != nil || !running {
			observed = "failed"
			errorCode = "VMM_NOT_RUNNING"
		}
	}
	return &nodev1.SandboxInspection{SandboxId: record.SandboxID, OwnershipGeneration: record.Generation, ObservedState: observed, BootId: record.BootID, GuestEpoch: record.GuestEpoch, ErrorCode: errorCode, ObservedAt: timestamppb.Now()}, nil
}
func (s *CHVServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.workers.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, instance := range s.live {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := instance.Stop(ctx)
		cancel()
		instance.Close()
		record, _ := s.journal.getSandbox(id)
		record.State = "stopped"
		record.BootID = ""
		record.GuestEpoch = ""
		if err != nil {
			record.State = "failed"
		}
		if err := s.journal.saveSandbox(record); err != nil {
			slog.Error("persist CHV shutdown state", "error", err)
		}
	}
	s.endLife()
	_ = s.stateLock.Close()
}

// Keep immutable snapshots at the CHV boundary even when another provider's
// journal implementation retains its input pointers.
func cloneCHVEntry(entry journalOperation) journalOperation {
	return journalOperation{Operation: proto.Clone(entry.Operation).(*nodev1.Operation), Request: proto.Clone(entry.Request).(*nodev1.ApplyOperationRequest)}
}
func (s *CHVServer) persistOperation(entry journalOperation) error {
	return s.journal.saveOperation(cloneCHVEntry(entry))
}
func (s *CHVServer) persistIntent(entry journalOperation, record sandboxRecord) error {
	return s.journal.saveIntent(cloneCHVEntry(entry), record)
}
