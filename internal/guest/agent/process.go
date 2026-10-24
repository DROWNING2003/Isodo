package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxCommandArgs         = 256
	maxCommandBytes        = 128 << 10
	maxRunningProcesses    = 32
	maxProcessOutputBytes  = 1 << 20
	maxProcessOutputEvents = 256
	processResultRetention = 30 * time.Second
	processDedupRetention  = 10 * time.Minute
)

type guestProcess struct {
	mu               sync.Mutex
	commandHash      [sha256.Size]byte
	requestID        string
	command          *exec.Cmd
	startedAt        time.Time
	state            guestv1.ProcessState
	exit             *guestv1.ProcessExit
	exitedAt         time.Time
	timedOut         bool
	outputIncomplete bool
	firstSeq         uint64
	nextSeq          uint64
	outputBytes      int
	events           []*guestv1.ProcessEvent
	changed          chan struct{}
	stdoutDone       chan struct{}
	stderrDone       chan struct{}
}

type processRequestRecord struct {
	commandHash [sha256.Size]byte
	executionID string
	expiresAt   time.Time
}

func (s *Server) StartProcess(ctx context.Context, request *guestv1.StartProcessRequest) (*guestv1.ProcessInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.GetRequestId()); err != nil {
		return nil, err
	}
	command := request.GetCommand().GetArgv()
	if len(command) == 0 || len(command) > maxCommandArgs || command[0] == "" {
		return nil, status.Error(codes.InvalidArgument, "INVALID_COMMAND: command argv is required and limited to 256 arguments")
	}
	commandBytes := 0
	for _, arg := range command {
		if strings.ContainsRune(arg, 0) || !utf8.ValidString(arg) {
			return nil, status.Error(codes.InvalidArgument, "INVALID_COMMAND: argv contains invalid text")
		}
		commandBytes += len(arg)
	}
	if request.GetPty() != nil {
		return nil, status.Error(codes.Unimplemented, "CAPABILITY_UNSUPPORTED: PTY is not implemented")
	}
	if request.GetStdinOpen() {
		return nil, status.Error(codes.Unimplemented, "CAPABILITY_UNSUPPORTED: interactive stdin is not implemented")
	}
	if err := validateProcessEnvironment(request.GetEnv(), &commandBytes); err != nil {
		return nil, err
	}
	if commandBytes > maxCommandBytes {
		return nil, status.Error(codes.InvalidArgument, "INVALID_COMMAND: argv and env exceed 128 KiB")
	}
	cwd := request.GetCwd()
	if cwd == "" {
		cwd = s.workspacePath()
	}
	if !filepath.IsAbs(cwd) || strings.ContainsRune(cwd, 0) {
		return nil, status.Error(codes.InvalidArgument, "INVALID_CWD: cwd must be an absolute Guest path")
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil, status.Error(codes.InvalidArgument, "INVALID_CWD: cwd must name an existing directory")
	}
	timeout, err := commandTimeout(request)
	if err != nil {
		return nil, err
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot canonicalize process request")
	}
	commandHash := sha256.Sum256(canonical)

	s.processMu.Lock()
	s.cleanupProcessesLocked(time.Now())
	if s.processes == nil {
		s.processes = make(map[string]*guestProcess)
	}
	if s.processRequests == nil {
		s.processRequests = make(map[string]processRequestRecord)
	}
	if prior, ok := s.processRequests[request.GetRequestId()]; ok {
		if prior.commandHash != commandHash {
			s.processMu.Unlock()
			return nil, status.Error(codes.AlreadyExists, "IDEMPOTENCY_CONFLICT: request_id was used with a different command")
		}
		process := s.processes[prior.executionID]
		s.processMu.Unlock()
		if process == nil {
			return nil, status.Error(codes.FailedPrecondition, "EXECUTION_EXPIRED: process result is no longer retained")
		}
		return process.info(s.currentIdentity(), prior.executionID), nil
	}
	running := 0
	for _, process := range s.processes {
		process.mu.Lock()
		if process.state == guestv1.ProcessState_PROCESS_STATE_RUNNING {
			running++
		}
		process.mu.Unlock()
	}
	if running >= maxRunningProcesses {
		s.processMu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "PROCESS_LIMIT: maximum concurrent Guest processes reached")
	}
	if len(s.processRequests) >= 4096 {
		s.processMu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "REQUEST_LIMIT: process deduplication records are full")
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		s.processMu.Unlock()
		return nil, status.Errorf(codes.Internal, "create stdout pipe: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		s.processMu.Unlock()
		return nil, status.Errorf(codes.Internal, "create stderr pipe: %v", err)
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = cwd
	cmd.Env = processEnvironment(request.GetEnv())
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		s.processMu.Unlock()
		return nil, status.Errorf(codes.NotFound, "COMMAND_START_FAILED: start process: %v", err)
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	executionID, err := newExecutionID()
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		s.processMu.Unlock()
		return nil, status.Errorf(codes.Internal, "generate execution ID: %v", err)
	}
	process := &guestProcess{
		commandHash: commandHash,
		requestID:   request.GetRequestId(),
		command:     cmd,
		startedAt:   time.Now().UTC(),
		state:       guestv1.ProcessState_PROCESS_STATE_RUNNING,
		firstSeq:    1,
		nextSeq:     1,
		changed:     make(chan struct{}),
		stdoutDone:  make(chan struct{}),
		stderrDone:  make(chan struct{}),
	}
	s.processes[executionID] = process
	s.processRequests[request.GetRequestId()] = processRequestRecord{
		commandHash: commandHash,
		executionID: executionID,
		expiresAt:   time.Now().Add(processDedupRetention),
	}
	s.processMu.Unlock()

	go process.pump(stdoutReader, guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT, process.stdoutDone)
	go process.pump(stderrReader, guestv1.OutputChannel_OUTPUT_CHANNEL_STDERR, process.stderrDone)
	go s.waitProcess(executionID, process, stdoutReader, stderrReader)
	if timeout > 0 {
		go s.timeoutProcess(executionID, process, timeout)
	}
	return process.info(s.currentIdentity(), executionID), nil
}

func (s *Server) GetProcess(ctx context.Context, reference *guestv1.ProcessRef) (*guestv1.ProcessInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	process, executionID, err := s.lookupProcess(reference)
	if err != nil {
		return nil, err
	}
	return process.info(s.currentIdentity(), executionID), nil
}

func (s *Server) ListProcesses(ctx context.Context, request *guestv1.ListProcessesRequest) (*guestv1.ListProcessesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	pageSize := int(request.GetPageSize())
	if pageSize == 0 {
		pageSize = 100
	}
	if pageSize < 1 || pageSize > 500 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be between 1 and 500")
	}
	s.processMu.Lock()
	s.cleanupProcessesLocked(time.Now())
	ids := make([]string, 0, len(s.processes))
	for executionID := range s.processes {
		ids = append(ids, executionID)
	}
	s.processMu.Unlock()
	sort.Strings(ids)
	if request.GetPageToken() != "" {
		index := sort.SearchStrings(ids, request.GetPageToken())
		if index == len(ids) || ids[index] != request.GetPageToken() {
			return nil, status.Error(codes.FailedPrecondition, "PAGE_EXPIRED: process page token is no longer available")
		}
		ids = ids[index+1:]
	}
	end := min(pageSize, len(ids))
	response := &guestv1.ListProcessesResponse{Processes: make([]*guestv1.ProcessInfo, 0, end)}
	for _, executionID := range ids[:end] {
		s.processMu.Lock()
		process := s.processes[executionID]
		s.processMu.Unlock()
		if process != nil {
			response.Processes = append(response.Processes, process.info(s.currentIdentity(), executionID))
		}
	}
	if end < len(ids) && end > 0 {
		response.NextPageToken = ids[end-1]
	}
	return response, nil
}

func (s *Server) SubscribeProcess(request *guestv1.SubscribeProcessRequest, stream guestv1.GuestService_SubscribeProcessServer) error {
	if request == nil || request.GetRef() == nil {
		return status.Error(codes.InvalidArgument, "process reference is required")
	}
	process, executionID, err := s.lookupProcess(request.GetRef())
	if err != nil {
		return err
	}
	if err := stream.Send(&guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Attached{Attached: &guestv1.Attached{Process: process.info(s.currentIdentity(), executionID)}}}); err != nil {
		return err
	}
	cursor := uint64(0)
	if request.AfterSeq != nil {
		cursor = request.GetAfterSeq()
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		process.mu.Lock()
		maximumSeq := process.nextSeq - 1
		if cursor > maximumSeq {
			process.mu.Unlock()
			return status.Error(codes.OutOfRange, "INVALID_CURSOR: after_seq is ahead of produced output")
		}
		if len(process.events) > 0 && cursor+1 < process.events[0].GetSeq() {
			firstMissing := cursor + 1
			lastMissing := process.events[0].GetSeq() - 1
			changed := process.changed
			process.mu.Unlock()
			if err := stream.Send(&guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Gap{Gap: &guestv1.OutputGap{FirstMissingSeq: firstMissing, LastMissingSeq: lastMissing}}}); err != nil {
				return err
			}
			cursor = lastMissing
			_ = changed
			continue
		}
		var pending []*guestv1.ProcessEvent
		for _, event := range process.events {
			if event.GetSeq() > cursor {
				pending = append(pending, proto.Clone(event).(*guestv1.ProcessEvent))
			}
		}
		changed := process.changed
		exited := process.state == guestv1.ProcessState_PROCESS_STATE_EXITED
		process.mu.Unlock()
		for _, event := range pending {
			if err := stream.Send(event); err != nil {
				return err
			}
			cursor = event.GetSeq()
		}
		if exited && len(pending) == 0 {
			return nil
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-changed:
		case <-heartbeat.C:
			if err := stream.Send(&guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Heartbeat{Heartbeat: &guestv1.Heartbeat{}}}); err != nil {
				return err
			}
		}
	}
}

func (s *Server) SignalProcess(ctx context.Context, request *guestv1.SignalProcessRequest) (*emptypb.Empty, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.GetRef() == nil {
		return nil, status.Error(codes.InvalidArgument, "process reference is required")
	}
	process, _, err := s.lookupProcess(request.GetRef())
	if err != nil {
		return nil, err
	}
	var signal syscall.Signal
	switch request.GetSignal() {
	case guestv1.ProcessSignal_PROCESS_SIGNAL_INTERRUPT:
		signal = syscall.SIGINT
	case guestv1.ProcessSignal_PROCESS_SIGNAL_TERMINATE:
		signal = syscall.SIGTERM
	case guestv1.ProcessSignal_PROCESS_SIGNAL_KILL:
		signal = syscall.SIGKILL
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported process signal")
	}
	process.mu.Lock()
	if process.state == guestv1.ProcessState_PROCESS_STATE_EXITED {
		process.mu.Unlock()
		return new(emptypb.Empty), nil
	}
	pid := process.command.Process.Pid
	process.mu.Unlock()
	if err := syscall.Kill(-pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return nil, status.Errorf(codes.FailedPrecondition, "signal process group: %v", err)
	}
	if signal == syscall.SIGTERM {
		go func() {
			time.Sleep(2 * time.Second)
			process.mu.Lock()
			stillRunning := process.state == guestv1.ProcessState_PROCESS_STATE_RUNNING
			process.mu.Unlock()
			if stillRunning {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
	}
	return new(emptypb.Empty), nil
}

func (s *Server) waitProcess(executionID string, process *guestProcess, stdout, stderr *os.File) {
	err := process.command.Wait()
	process.mu.Lock()
	processExited := process.state == guestv1.ProcessState_PROCESS_STATE_RUNNING
	process.mu.Unlock()
	if processExited {
		// The command's process group is the workload boundary for this rootless
		// Lima Guest implementation; reap short-lived shell descendants on exit.
		_ = syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
	}
	stdoutDone, stderrDone := process.stdoutDone, process.stderrDone
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for stdoutDone != nil || stderrDone != nil {
		select {
		case <-stdoutDone:
			stdoutDone = nil
		case <-stderrDone:
			stderrDone = nil
		case <-deadline.C:
			process.mu.Lock()
			process.outputIncomplete = true
			process.mu.Unlock()
			_ = stdout.Close()
			_ = stderr.Close()
			if stdoutDone != nil {
				<-stdoutDone
				stdoutDone = nil
			}
			if stderrDone != nil {
				<-stderrDone
				stderrDone = nil
			}
		}
	}
	process.mu.Lock()
	process.state = guestv1.ProcessState_PROCESS_STATE_EXITED
	process.exitedAt = time.Now().UTC()
	process.exit = processExit(err, process.timedOut, process.outputIncomplete)
	process.appendEventLocked(&guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Exit{Exit: proto.Clone(process.exit).(*guestv1.ProcessExit)}})
	process.notifyLocked()
	process.mu.Unlock()
	s.processMu.Lock()
	if request, ok := s.processRequests[process.requestID]; ok {
		request.expiresAt = process.exitedAt.Add(processDedupRetention)
		s.processRequests[process.requestID] = request
	}
	s.processMu.Unlock()
	_ = executionID
}

func (s *Server) timeoutProcess(_ string, process *guestProcess, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	<-timer.C
	process.mu.Lock()
	if process.state != guestv1.ProcessState_PROCESS_STATE_RUNNING {
		process.mu.Unlock()
		return
	}
	process.timedOut = true
	pid := process.command.Process.Pid
	process.mu.Unlock()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	process.mu.Lock()
	stillRunning := process.state == guestv1.ProcessState_PROCESS_STATE_RUNNING
	process.mu.Unlock()
	if stillRunning {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

func (s *Server) lookupProcess(reference *guestv1.ProcessRef) (*guestProcess, string, error) {
	if reference == nil || reference.GetExecutionId() == "" {
		return nil, "", status.Error(codes.InvalidArgument, "execution ID is required")
	}
	if err := s.checkIdentity(reference.GetIdentity()); err != nil {
		return nil, "", err
	}
	s.processMu.Lock()
	s.cleanupProcessesLocked(time.Now())
	process := s.processes[reference.GetExecutionId()]
	s.processMu.Unlock()
	if process == nil {
		return nil, "", status.Error(codes.NotFound, "PROCESS_NOT_FOUND: execution ID is not retained")
	}
	return process, reference.GetExecutionId(), nil
}

func (s *Server) cleanupProcessesLocked(now time.Time) {
	for id, process := range s.processes {
		process.mu.Lock()
		expired := process.state == guestv1.ProcessState_PROCESS_STATE_EXITED && now.Sub(process.exitedAt) > processResultRetention
		process.mu.Unlock()
		if expired {
			delete(s.processes, id)
		}
	}
	for requestID, request := range s.processRequests {
		if !now.Before(request.expiresAt) {
			delete(s.processRequests, requestID)
		}
	}
}

func (s *Server) currentIdentity() *guestv1.RuntimeIdentity {
	s.mu.RLock()
	identity := s.identity
	s.mu.RUnlock()
	return &guestv1.RuntimeIdentity{SandboxId: identity.SandboxID, OwnershipGeneration: identity.OwnershipGeneration, BootId: identity.BootID, GuestEpoch: identity.GuestEpoch}
}

func (process *guestProcess) pump(reader *os.File, channel guestv1.OutputChannel, done chan<- struct{}) {
	defer close(done)
	defer reader.Close()
	buffer := make([]byte, maxChunkBytes)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			process.mu.Lock()
			process.appendEventLocked(&guestv1.ProcessEvent{Event: &guestv1.ProcessEvent_Output{Output: &guestv1.ProcessOutput{Channel: channel, Data: append([]byte(nil), buffer[:count]...)}}})
			process.notifyLocked()
			process.mu.Unlock()
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			process.mu.Lock()
			process.outputIncomplete = true
			process.notifyLocked()
			process.mu.Unlock()
			return
		}
	}
}

func (process *guestProcess) appendEventLocked(event *guestv1.ProcessEvent) {
	if process.nextSeq == 0 {
		process.nextSeq = 1
	}
	event.Seq = process.nextSeq
	process.nextSeq++
	if output := event.GetOutput(); output != nil {
		process.outputBytes += len(output.GetData())
	}
	process.events = append(process.events, event)
	for process.outputBytes > maxProcessOutputBytes || len(process.events) > maxProcessOutputEvents {
		first := process.events[0]
		if first.GetExit() != nil {
			break
		}
		if output := first.GetOutput(); output != nil {
			process.outputBytes -= len(output.GetData())
		}
		process.events = process.events[1:]
	}
	if len(process.events) == 0 {
		process.firstSeq = process.nextSeq
	} else {
		process.firstSeq = process.events[0].GetSeq()
	}
}

func (process *guestProcess) notifyLocked() {
	close(process.changed)
	process.changed = make(chan struct{})
}

func (process *guestProcess) info(identity *guestv1.RuntimeIdentity, executionID string) *guestv1.ProcessInfo {
	process.mu.Lock()
	defer process.mu.Unlock()
	info := &guestv1.ProcessInfo{
		Ref:               &guestv1.ProcessRef{Identity: proto.Clone(identity).(*guestv1.RuntimeIdentity), ExecutionId: executionID},
		Pid:               uint32(process.command.Process.Pid),
		State:             process.state,
		StartedAt:         timestamppb.New(process.startedAt),
		FirstAvailableSeq: process.firstSeq,
		NextSeq:           process.nextSeq,
	}
	if process.exit != nil {
		info.Result = proto.Clone(process.exit).(*guestv1.ProcessExit)
		info.ResultExpiresAt = timestamppb.New(process.exitedAt.Add(processResultRetention))
	}
	return info
}

func processExit(err error, timedOut, outputIncomplete bool) *guestv1.ProcessExit {
	result := &guestv1.ProcessExit{Reason: guestv1.ExitReason_EXIT_REASON_COMPLETED, ExitedAt: timestamppb.Now(), OutputIncomplete: outputIncomplete}
	if timedOut {
		result.Reason = guestv1.ExitReason_EXIT_REASON_COMMAND_TIMEOUT
	}
	if err == nil {
		code := int32(0)
		result.ExitCode = &code
		return result
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		result.Reason = guestv1.ExitReason_EXIT_REASON_SIGNALED
		return result
	}
	if wait, ok := exitError.Sys().(syscall.WaitStatus); ok && wait.Signaled() {
		signal := uint32(wait.Signal())
		result.Signal = &signal
		if !timedOut {
			result.Reason = guestv1.ExitReason_EXIT_REASON_SIGNALED
		}
		return result
	}
	code := int32(exitError.ExitCode())
	result.ExitCode = &code
	return result
}

func validateProcessEnvironment(environment map[string]string, totalBytes *int) error {
	for key, value := range environment {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) || !utf8.ValidString(key) || !utf8.ValidString(value) {
			return status.Error(codes.InvalidArgument, "INVALID_ENV: environment contains an invalid key or value")
		}
		*totalBytes += len(key) + len(value)
	}
	return nil
}

func processEnvironment(overrides map[string]string) []string {
	values := map[string]string{
		"PATH": "/usr/local/bin:/usr/bin:/bin",
		"HOME": "/workspace/.home",
		"USER": "nobody",
		"LANG": "C.UTF-8",
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func commandTimeout(request *guestv1.StartProcessRequest) (time.Duration, error) {
	if request.CommandTimeoutSeconds == nil {
		return 60 * time.Second, nil
	}
	seconds := request.GetCommandTimeoutSeconds()
	if seconds > 86400 {
		return 0, status.Error(codes.InvalidArgument, "command_timeout_seconds must not exceed 86400")
	}
	return time.Duration(seconds) * time.Second, nil
}

func newExecutionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate execution ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *Server) workspacePath() string {
	if s.workspace == "" {
		return defaultWorkspace
	}
	return s.workspace
}
