package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/lima"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type LimaServer struct {
	nodev1.UnimplementedNodeOrchestratorServer
	status      *StatusServer
	manager     *lima.Manager
	t3Bundle    string
	journal     *limaJournal
	applyMu     sync.Mutex
	inFlight    map[string]string
	workerCtx   context.Context
	stopWorkers context.CancelFunc
	workers     sync.WaitGroup
}

type sandboxRecord struct {
	SandboxID       string         `json:"sandbox_id"`
	Generation      uint64         `json:"generation"`
	Revision        uint64         `json:"revision"`
	State           string         `json:"state"`
	Instance        string         `json:"instance"`
	TemplateID      string         `json:"template_id,omitempty"`
	TemplateBuildID string         `json:"template_build_id,omitempty"`
	BootID          string         `json:"boot_id,omitempty"`
	GuestEpoch      string         `json:"guest_epoch,omitempty"`
	Resources       lima.Resources `json:"resources"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type journalOperation struct {
	Operation *nodev1.Operation
	Request   *nodev1.ApplyOperationRequest
}

type journalDiskOperation struct {
	Operation json.RawMessage `json:"operation"`
	Request   json.RawMessage `json:"request"`
}

type journalDisk struct {
	Version    int                             `json:"version"`
	Operations map[string]journalDiskOperation `json:"operations"`
	Sandboxes  map[string]sandboxRecord        `json:"sandboxes"`
}

type limaJournal struct {
	mu         sync.Mutex
	path       string
	operations map[string]journalOperation
	sandboxes  map[string]sandboxRecord
}

func NewLimaServer(nodeStatus NodeStatus, manager *lima.Manager, journalPath string, t3BundlePath ...string) (*LimaServer, error) {
	if manager == nil {
		return nil, errors.New("Lima manager is required")
	}
	if len(t3BundlePath) > 1 {
		return nil, errors.New("at most one T3 server bundle path may be provided")
	}
	statusServer, err := NewStatusServer(nodeStatus)
	if err != nil {
		return nil, err
	}
	journal, err := openLimaJournal(journalPath)
	if err != nil {
		return nil, err
	}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	server := &LimaServer{
		status: statusServer, manager: manager, journal: journal,
		inFlight: make(map[string]string), workerCtx: workerCtx, stopWorkers: stopWorkers,
	}
	if len(t3BundlePath) == 1 {
		server.t3Bundle = t3BundlePath[0]
	}
	if err := server.recover(); err != nil {
		stopWorkers()
		return nil, fmt.Errorf("recover Lima operations: %w", err)
	}
	return server, nil
}

func (s *LimaServer) Close() {
	s.stopWorkers()
	s.workers.Wait()
}

func openLimaJournal(path string) (*limaJournal, error) {
	if path == "" {
		return nil, errors.New("Lima journal path is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Lima journal path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Lima journal directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("restrict Lima journal directory permissions: %w", err)
	}
	journal := &limaJournal{
		path:       path,
		operations: make(map[string]journalOperation),
		sandboxes:  make(map[string]sandboxRecord),
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Lima journal: %w", err)
	}
	var disk journalDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("decode Lima journal: %w", err)
	}
	if disk.Version != 1 {
		return nil, fmt.Errorf("unsupported Lima journal version %d", disk.Version)
	}
	for id, entry := range disk.Operations {
		var operation nodev1.Operation
		var request nodev1.ApplyOperationRequest
		if err := protojson.Unmarshal(entry.Operation, &operation); err != nil {
			return nil, fmt.Errorf("decode operation %q in Lima journal: %w", id, err)
		}
		if err := protojson.Unmarshal(entry.Request, &request); err != nil {
			return nil, fmt.Errorf("decode request %q in Lima journal: %w", id, err)
		}
		journal.operations[id] = journalOperation{Operation: &operation, Request: &request}
	}
	if disk.Sandboxes != nil {
		journal.sandboxes = disk.Sandboxes
	}
	return journal, nil
}

func (j *limaJournal) persistLocked() error {
	disk := journalDisk{
		Version:    1,
		Operations: make(map[string]journalDiskOperation, len(j.operations)),
		Sandboxes:  j.sandboxes,
	}
	for id, entry := range j.operations {
		operation, err := protojson.Marshal(entry.Operation)
		if err != nil {
			return fmt.Errorf("encode operation %q: %w", id, err)
		}
		request, err := protojson.Marshal(entry.Request)
		if err != nil {
			return fmt.Errorf("encode request %q: %w", id, err)
		}
		disk.Operations[id] = journalDiskOperation{Operation: operation, Request: request}
	}
	data, err := json.Marshal(disk)
	if err != nil {
		return fmt.Errorf("encode Lima journal: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(j.path), ".lima-journal-*")
	if err != nil {
		return fmt.Errorf("create temporary Lima journal: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict Lima journal permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write Lima journal: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync Lima journal: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Lima journal: %w", err)
	}
	if err := os.Rename(temporaryName, j.path); err != nil {
		return fmt.Errorf("replace Lima journal: %w", err)
	}
	directory, err := os.Open(filepath.Dir(j.path))
	if err != nil {
		return fmt.Errorf("open Lima journal directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync Lima journal directory: %w", err)
	}
	return nil
}

func (j *limaJournal) saveOperation(entry journalOperation) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	previous, hadPrevious := j.operations[entry.Operation.OperationId]
	if entry.Operation.CreatedAt == nil {
		entry.Operation.CreatedAt = timestamppb.Now()
	}
	entry.Operation.UpdatedAt = timestamppb.Now()
	if hadPrevious {
		entry.Operation.EventSequence = previous.Operation.EventSequence + 1
	} else {
		entry.Operation.EventSequence = 1
	}
	j.operations[entry.Operation.OperationId] = entry
	if err := j.persistLocked(); err != nil {
		if hadPrevious {
			j.operations[entry.Operation.OperationId] = previous
		} else {
			delete(j.operations, entry.Operation.OperationId)
		}
		return err
	}
	return nil
}

func (j *limaJournal) saveSandbox(record sandboxRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	previous, hadPrevious := j.sandboxes[record.SandboxID]
	record.UpdatedAt = time.Now().UTC()
	j.sandboxes[record.SandboxID] = record
	if err := j.persistLocked(); err != nil {
		if hadPrevious {
			j.sandboxes[record.SandboxID] = previous
		} else {
			delete(j.sandboxes, record.SandboxID)
		}
		return err
	}
	return nil
}

func (j *limaJournal) saveIntent(entry journalOperation, record sandboxRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if entry.Operation.CreatedAt == nil {
		entry.Operation.CreatedAt = timestamppb.Now()
	}
	entry.Operation.UpdatedAt = timestamppb.Now()
	entry.Operation.EventSequence = 1
	record.UpdatedAt = time.Now().UTC()
	previousOperation, hadOperation := j.operations[entry.Operation.OperationId]
	previousSandbox, hadSandbox := j.sandboxes[record.SandboxID]
	j.operations[entry.Operation.OperationId] = entry
	j.sandboxes[record.SandboxID] = record
	if err := j.persistLocked(); err != nil {
		if hadOperation {
			j.operations[entry.Operation.OperationId] = previousOperation
		} else {
			delete(j.operations, entry.Operation.OperationId)
		}
		if hadSandbox {
			j.sandboxes[record.SandboxID] = previousSandbox
		} else {
			delete(j.sandboxes, record.SandboxID)
		}
		return err
	}
	return nil
}

func (j *limaJournal) getOperation(id string) (journalOperation, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	entry, ok := j.operations[id]
	if !ok {
		return journalOperation{}, false
	}
	return journalOperation{
		Operation: proto.Clone(entry.Operation).(*nodev1.Operation),
		Request:   proto.Clone(entry.Request).(*nodev1.ApplyOperationRequest),
	}, true
}

func (j *limaJournal) getSandbox(id string) (sandboxRecord, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.sandboxes[id]
	return record, ok
}

func (j *limaJournal) allOperations() []journalOperation {
	j.mu.Lock()
	defer j.mu.Unlock()
	operations := make([]journalOperation, 0, len(j.operations))
	for _, entry := range j.operations {
		operations = append(operations, journalOperation{
			Operation: proto.Clone(entry.Operation).(*nodev1.Operation),
			Request:   proto.Clone(entry.Request).(*nodev1.ApplyOperationRequest),
		})
	}
	return operations
}

func (s *LimaServer) GetNodeStatus(ctx context.Context, request *emptypb.Empty) (*nodev1.NodeStatus, error) {
	return s.status.GetNodeStatus(ctx, request)
}

func (s *LimaServer) ApplyOperation(ctx context.Context, request *nodev1.ApplyOperationRequest) (*nodev1.ApplyOperationResponse, error) {
	if request == nil || request.OperationId == "" || request.RequestHash == "" || request.SandboxId == "" || request.OwnershipGeneration == 0 || request.Revision == 0 {
		return nil, status.Error(codes.InvalidArgument, "operation ID, request hash, sandbox ID, generation, and revision are required")
	}
	if err := s.validateRequest(request); err != nil {
		return nil, err
	}
	if _, err := s.manager.NameForSandbox(request.SandboxId); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if existing, ok := s.journal.getOperation(request.OperationId); ok {
		if existing.Operation.RequestHash != request.RequestHash {
			return nil, status.Error(codes.AlreadyExists, "operation ID was already used with a different request hash")
		}
		return &nodev1.ApplyOperationResponse{Operation: existing.Operation, AlreadyApplied: true}, nil
	}
	if operationID, ok := s.inFlight[request.SandboxId]; ok {
		return nil, status.Errorf(codes.Aborted, "Sandbox already has active operation %q", operationID)
	}

	record, exists := s.journal.getSandbox(request.SandboxId)
	if err := s.validateFencing(request, record, exists); err != nil {
		return nil, err
	}
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE {
		_, nameErr := s.manager.NameForSandbox(request.SandboxId)
		if nameErr != nil {
			return nil, status.Error(codes.InvalidArgument, nameErr.Error())
		}
		if _, err := s.manager.Inspect(ctx, request.SandboxId); err == nil {
			if !exists || record.State == "deleted" {
				return nil, status.Error(codes.AlreadyExists, "a Lima instance with this Sandbox identity exists outside the active Node generation")
			}
		} else if !errors.Is(err, lima.ErrInstanceNotFound) {
			return nil, status.Errorf(codes.Internal, "inspect Lima instance before create: %v", err)
		}
	}
	resources := s.requestResources(request, record, exists)
	if resources == nil {
		return nil, status.Error(codes.FailedPrecondition, "Lima provider only accepts its configured per-Node CPU, memory, and disk allocation")
	}
	if !exists || request.OwnershipGeneration > record.Generation {
		instanceName, err := s.manager.NameForSandbox(request.SandboxId)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		initialState := "creating"
		if request.Action == nodev1.OperationAction_OPERATION_ACTION_DELETE {
			initialState = "deleted"
		}
		record = sandboxRecord{
			SandboxID: request.SandboxId, Generation: request.OwnershipGeneration,
			Instance: instanceName, TemplateID: request.TemplateId,
			TemplateBuildID: request.TemplateBuildId, Resources: *resources, State: initialState,
		}
	}
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE {
		record.TemplateID = request.TemplateId
		record.TemplateBuildID = request.TemplateBuildId
	}
	record.Revision = request.Revision
	operation := &nodev1.Operation{
		OperationId: request.OperationId, RequestHash: request.RequestHash, SandboxId: request.SandboxId,
		OwnershipGeneration: request.OwnershipGeneration, Revision: request.Revision, Action: request.Action,
		State: nodev1.OperationState_OPERATION_STATE_ACCEPTED, Stage: nodev1.OperationStage_OPERATION_STAGE_INTENT,
		BootId: s.status.status.BootID,
	}
	entry := journalOperation{Operation: operation, Request: request}
	if err := s.journal.saveIntent(entry, record); err != nil {
		return nil, status.Errorf(codes.Internal, "persist operation and Sandbox ownership intent: %v", err)
	}
	s.inFlight[request.SandboxId] = request.OperationId
	s.launchOperation(entry, record, false)
	accepted, _ := s.journal.getOperation(request.OperationId)
	return &nodev1.ApplyOperationResponse{Operation: accepted.Operation}, nil
}

func (s *LimaServer) launchOperation(entry journalOperation, record sandboxRecord, resume bool) {
	s.workers.Go(func() {
		finished := false
		defer func() {
			if !finished {
				return
			}
			s.applyMu.Lock()
			delete(s.inFlight, entry.Operation.SandboxId)
			s.applyMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.workerCtx, 30*time.Minute)
		defer cancel()
		if err := s.runOperation(ctx, entry, record, resume); err != nil {
			slog.Error("Lima operation could not be committed to the local journal", "operation_id", entry.Operation.OperationId, "sandbox_id", entry.Operation.SandboxId, "error", err)
			return
		}
		latest, ok := s.journal.getOperation(entry.Operation.OperationId)
		if !ok || !terminal(latest.Operation.State) {
			slog.Error("Lima operation returned without a durable terminal state", "operation_id", entry.Operation.OperationId, "sandbox_id", entry.Operation.SandboxId)
			return
		}
		finished = true
	})
}

func (s *LimaServer) validateRequest(request *nodev1.ApplyOperationRequest) error {
	if request.Action < nodev1.OperationAction_OPERATION_ACTION_CREATE || request.Action > nodev1.OperationAction_OPERATION_ACTION_DELETE {
		return status.Error(codes.InvalidArgument, "unsupported Lima operation action")
	}
	if (request.TemplateId == "") != (request.TemplateBuildId == "") {
		return status.Error(codes.InvalidArgument, "template ID and template build ID must be provided together")
	}
	if request.Action != nodev1.OperationAction_OPERATION_ACTION_CREATE && (request.TemplateId != "" || request.TemplateBuildId != "") {
		return status.Error(codes.InvalidArgument, "template selection is valid only when creating a Sandbox")
	}
	if request.Network != nil {
		if request.Network.EgressMode == nodev1.EgressMode_EGRESS_MODE_DENY || len(request.Network.AllowedCidrs) > 0 {
			return status.Error(codes.FailedPrecondition, "Lima provider does not enforce egress-deny or CIDR allow-list policies")
		}
	}
	return nil
}

func (s *LimaServer) requestResources(request *nodev1.ApplyOperationRequest, record sandboxRecord, exists bool) *lima.Resources {
	if exists && record.Resources.CPUs > 0 {
		if request.Resources != nil && !matchesResources(request.Resources, record.Resources) {
			return nil
		}
		return &record.Resources
	}
	defaults := s.manager.DefaultResources()
	if request.Resources != nil && !matchesResources(request.Resources, defaults) {
		return nil
	}
	return &defaults
}

func matchesResources(request *nodev1.ResourceSpec, resources lima.Resources) bool {
	if request.Vcpu == 0 && request.MemoryBytes == 0 && request.DiskBytes == 0 {
		return true
	}
	return request.Vcpu == uint32(resources.CPUs) && request.MemoryBytes == resources.MemoryBytes && request.DiskBytes == resources.DiskBytes
}

func (s *LimaServer) validateFencing(request *nodev1.ApplyOperationRequest, record sandboxRecord, exists bool) error {
	if !exists {
		if request.Action != nodev1.OperationAction_OPERATION_ACTION_CREATE && request.Action != nodev1.OperationAction_OPERATION_ACTION_DELETE {
			return status.Error(codes.NotFound, "Sandbox ownership is not registered on this Node")
		}
		return nil
	}
	if request.OwnershipGeneration < record.Generation {
		return status.Error(codes.Aborted, "operation ownership generation is stale")
	}
	if request.OwnershipGeneration > record.Generation {
		if request.Action != nodev1.OperationAction_OPERATION_ACTION_CREATE || record.State != "deleted" {
			return status.Error(codes.FailedPrecondition, "a new generation can only be created after the previous generation is deleted")
		}
		return nil
	}
	if request.Revision <= record.Revision {
		return status.Error(codes.Aborted, "operation revision is stale")
	}
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE && record.State == "failed" &&
		(request.TemplateId != record.TemplateID || request.TemplateBuildId != record.TemplateBuildID) {
		return status.Error(codes.FailedPrecondition, "a failed Sandbox generation cannot switch its fixed template build")
	}
	if request.Action == nodev1.OperationAction_OPERATION_ACTION_CREATE && record.State != "deleted" && record.State != "failed" {
		return status.Error(codes.AlreadyExists, "Sandbox generation already exists on this Node")
	}
	switch request.Action {
	case nodev1.OperationAction_OPERATION_ACTION_START, nodev1.OperationAction_OPERATION_ACTION_STOP:
		if record.State != "running" && record.State != "stopped" && record.State != "failed" {
			return status.Error(codes.FailedPrecondition, "Sandbox is not in a state that supports start or stop")
		}
	}
	return nil
}

func (s *LimaServer) runOperation(ctx context.Context, entry journalOperation, record sandboxRecord, resume bool) error {
	operation := entry.Operation
	operation.State = nodev1.OperationState_OPERATION_STATE_RUNNING
	operation.Stage = stageForAction(operation.Action)
	operation.Message = "Lima operation is running"
	if err := s.journal.saveOperation(entry); err != nil {
		return err
	}

	var runErr error
	switch operation.Action {
	case nodev1.OperationAction_OPERATION_ACTION_CREATE:
		if resume {
			_, runErr = s.manager.StartFromTemplate(ctx, operation.SandboxId, entry.Request.GetTemplateId(), entry.Request.GetTemplateBuildId())
		} else {
			_, runErr = s.manager.CreateFromTemplate(ctx, operation.SandboxId, entry.Request.GetTemplateId(), entry.Request.GetTemplateBuildId())
		}
		if runErr == nil {
			record.BootID, record.GuestEpoch, runErr = s.provisionAndBindGuest(ctx, operation)
			if runErr == nil {
				runErr = s.startT3Server(ctx, operation, record.BootID, record.GuestEpoch)
			}
		}
	case nodev1.OperationAction_OPERATION_ACTION_START:
		_, runErr = s.manager.StartExisting(ctx, operation.SandboxId)
		if runErr == nil {
			record.BootID, record.GuestEpoch, runErr = s.provisionAndBindGuest(ctx, operation)
			if runErr == nil {
				runErr = s.startT3Server(ctx, operation, record.BootID, record.GuestEpoch)
			}
		}
	case nodev1.OperationAction_OPERATION_ACTION_STOP:
		runErr = s.manager.Stop(ctx, operation.SandboxId)
		record.BootID, record.GuestEpoch = "", ""
	case nodev1.OperationAction_OPERATION_ACTION_DELETE:
		if record.State != "deleted" {
			runErr = s.manager.Delete(ctx, operation.SandboxId)
		}
		record.BootID, record.GuestEpoch = "", ""
	default:
		runErr = errors.New("unsupported Lima operation")
	}
	if runErr != nil {
		if ctx.Err() != nil {
			if s.workerCtx.Err() != nil {
				return ctx.Err()
			}
			runErr = ctx.Err()
		}
		operation.State = nodev1.OperationState_OPERATION_STATE_FAILED
		operation.ErrorCode = "LIMA_OPERATION_FAILED"
		operation.Message = runErr.Error()
		record.State = "failed"
	} else {
		operation.State = nodev1.OperationState_OPERATION_STATE_SUCCEEDED
		operation.ErrorCode = ""
		switch operation.Action {
		case nodev1.OperationAction_OPERATION_ACTION_CREATE, nodev1.OperationAction_OPERATION_ACTION_START:
			record.State = "running"
			operation.BootId = record.BootID
			operation.Message = "Lima Guest Agent is ready and runtime identity is bound"
		case nodev1.OperationAction_OPERATION_ACTION_STOP:
			record.State = "stopped"
			operation.Message = "Lima VM is stopped and its disk is retained"
		case nodev1.OperationAction_OPERATION_ACTION_DELETE:
			record.State = "deleted"
			operation.Message = "Lima VM and its disk are deleted"
		}
	}
	record.Revision = operation.Revision
	if err := s.journal.saveSandbox(record); err != nil {
		return err
	}
	return s.journal.saveOperation(entry)
}

func (s *LimaServer) provisionAndBindGuest(ctx context.Context, operation *nodev1.Operation) (string, string, error) {
	bootID, err := s.manager.BootID(ctx, operation.SandboxId)
	if err != nil {
		return "", "", err
	}
	endpoint, err := s.manager.GuestEndpoint(operation.SandboxId)
	if err != nil {
		return "", "", err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := guesttransport.WaitTemplateReadyTCP(readyCtx, endpoint); err != nil {
		return "", "", fmt.Errorf("wait for Lima Guest Agent readiness: %w", err)
	}
	identity := &guestv1.RuntimeIdentity{
		SandboxId:           operation.SandboxId,
		OwnershipGeneration: operation.OwnershipGeneration,
		BootId:              bootID,
	}
	ready, err := guesttransport.BindRuntimeTCP(readyCtx, endpoint, identity)
	if err != nil {
		return "", "", fmt.Errorf("bind Lima Guest Agent to Sandbox: %w", err)
	}
	if ready.GetIdentity() == nil || ready.GetIdentity().GetGuestEpoch() == "" {
		return "", "", errors.New("Lima Guest Agent returned no guest epoch after binding")
	}
	return bootID, ready.GetIdentity().GetGuestEpoch(), nil
}

func stageForAction(action nodev1.OperationAction) nodev1.OperationStage {
	switch action {
	case nodev1.OperationAction_OPERATION_ACTION_CREATE, nodev1.OperationAction_OPERATION_ACTION_START:
		return nodev1.OperationStage_OPERATION_STAGE_LAUNCH
	case nodev1.OperationAction_OPERATION_ACTION_STOP:
		return nodev1.OperationStage_OPERATION_STAGE_STOP
	case nodev1.OperationAction_OPERATION_ACTION_DELETE:
		return nodev1.OperationStage_OPERATION_STAGE_DELETE
	default:
		return nodev1.OperationStage_OPERATION_STAGE_UNSPECIFIED
	}
}

func (s *LimaServer) GetOperation(_ context.Context, request *nodev1.GetOperationRequest) (*nodev1.Operation, error) {
	if request == nil || request.OperationId == "" {
		return nil, status.Error(codes.InvalidArgument, "operation ID is required")
	}
	entry, ok := s.journal.getOperation(request.OperationId)
	if !ok {
		return nil, status.Error(codes.NotFound, "operation was not found")
	}
	if request.RequestHash != "" && request.RequestHash != entry.Operation.RequestHash {
		return nil, status.Error(codes.FailedPrecondition, "operation request hash does not match")
	}
	return entry.Operation, nil
}

func (s *LimaServer) InspectSandbox(ctx context.Context, request *nodev1.InspectSandboxRequest) (*nodev1.SandboxInspection, error) {
	if request == nil || request.SandboxId == "" || request.OwnershipGeneration == 0 {
		return nil, status.Error(codes.InvalidArgument, "sandbox ID and ownership generation are required")
	}
	record, ok := s.journal.getSandbox(request.SandboxId)
	if !ok {
		return nil, status.Error(codes.NotFound, "Sandbox ownership was not found on this Node")
	}
	if request.OwnershipGeneration != record.Generation {
		return nil, status.Error(codes.Aborted, "inspection ownership generation is stale")
	}
	inspection := &nodev1.SandboxInspection{
		SandboxId: request.SandboxId, OwnershipGeneration: record.Generation,
		BootId: record.BootID, VmmInstanceId: record.Instance,
		ObservedAt: timestamppb.Now(),
	}
	if record.State == "deleted" {
		inspection.ObservedState = "deleted"
		return inspection, nil
	}
	instance, err := s.manager.Inspect(ctx, request.SandboxId)
	if errors.Is(err, lima.ErrInstanceNotFound) {
		inspection.ObservedState = "missing"
		inspection.ErrorCode = "LIMA_INSTANCE_MISSING"
		return inspection, nil
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "inspect Lima instance: %v", err)
	}
	inspection.ObservedState = strings.ToLower(instance.Status)
	return inspection, nil
}

func (s *LimaServer) WatchOperation(request *nodev1.WatchOperationRequest, stream nodev1.NodeOrchestrator_WatchOperationServer) error {
	if request == nil || request.OperationId == "" {
		return status.Error(codes.InvalidArgument, "operation ID is required")
	}
	sequence := request.AfterSequence
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		entry, ok := s.journal.getOperation(request.OperationId)
		if !ok {
			return status.Error(codes.NotFound, "operation was not found")
		}
		if terminal(entry.Operation.State) && entry.Operation.EventSequence <= sequence {
			return nil
		}
		if entry.Operation.EventSequence > sequence {
			if err := stream.Send(&nodev1.NodeEvent{Sequence: entry.Operation.EventSequence, NodeId: s.status.status.NodeID, Operation: entry.Operation}); err != nil {
				return err
			}
			sequence = entry.Operation.EventSequence
			if terminal(entry.Operation.State) {
				return nil
			}
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

func terminal(state nodev1.OperationState) bool {
	return state == nodev1.OperationState_OPERATION_STATE_SUCCEEDED || state == nodev1.OperationState_OPERATION_STATE_FAILED
}

func (s *LimaServer) recover() error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	for _, entry := range s.journal.allOperations() {
		if terminal(entry.Operation.State) {
			continue
		}
		if activeID, ok := s.inFlight[entry.Operation.SandboxId]; ok {
			return fmt.Errorf("multiple pending operations for Sandbox %q: %s and %s", entry.Operation.SandboxId, activeID, entry.Operation.OperationId)
		}
		record, ok := s.journal.getSandbox(entry.Operation.SandboxId)
		if !ok {
			return fmt.Errorf("operation %q has no Sandbox journal record", entry.Operation.OperationId)
		}
		entry.Operation.BootId = s.status.status.BootID
		if err := s.journal.saveOperation(entry); err != nil {
			return fmt.Errorf("update recovered operation %q: %w", entry.Operation.OperationId, err)
		}
		s.inFlight[entry.Operation.SandboxId] = entry.Operation.OperationId
		s.launchOperation(entry, record, true)
	}
	return nil
}
