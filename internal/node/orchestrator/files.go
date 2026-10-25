package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/sandboxfiles"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fileGuest struct {
	client   guestv1.GuestServiceClient
	identity *guestv1.RuntimeIdentity
	close    func()
	check    func() error
}
type guestFileBackend struct {
	open func(context.Context, *nodev1.SandboxTarget) (fileGuest, error)
}

func (s *CHVServer) FileBackend() sandboxfiles.Backend  { return guestFileBackend{open: s.fileGuest} }
func (s *LimaServer) FileBackend() sandboxfiles.Backend { return guestFileBackend{open: s.fileGuest} }
func checkFileRecord(record sandboxRecord, ok, busy bool, target *nodev1.SandboxTarget) error {
	if target == nil || target.SandboxId == "" || target.OwnershipGeneration == 0 || target.Revision == 0 {
		return status.Error(codes.InvalidArgument, "sandbox identity and revision are required")
	}
	if !ok || record.Generation != target.OwnershipGeneration {
		return status.Error(codes.NotFound, "sandbox ownership not found")
	}
	if busy || record.Revision != target.Revision || record.State != "running" || record.BootID == "" || record.GuestEpoch == "" {
		return status.Error(codes.FailedPrecondition, "sandbox instance is not available for Files")
	}
	return nil
}
func (s *CHVServer) fileGuest(_ context.Context, target *nodev1.SandboxTarget) (fileGuest, error) {
	s.mu.Lock()
	record, ok := s.journal.getSandbox(target.GetSandboxId())
	instance := s.live[target.GetSandboxId()]
	err := checkFileRecord(record, ok, s.busy[target.GetSandboxId()] || s.closed, target)
	if err == nil && instance == nil {
		err = status.Error(codes.FailedPrecondition, "sandbox runtime is unavailable")
	}
	s.mu.Unlock()
	if err != nil {
		return fileGuest{}, err
	}
	identity := instance.Identity()
	if identity.GetBootId() != record.BootID || identity.GetGuestEpoch() != record.GuestEpoch || identity.GetOwnershipGeneration() != record.Generation || identity.GetSandboxId() != record.SandboxID {
		return fileGuest{}, status.Error(codes.FailedPrecondition, "sandbox Guest identity changed")
	}
	connection, err := guesttransport.NewVsockClientConn(instance.VsockSocket())
	if err != nil {
		return fileGuest{}, err
	}
	return fileGuest{client: guestv1.NewGuestServiceClient(connection), identity: identity, close: func() { _ = connection.Close() }, check: func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		current, ok := s.journal.getSandbox(record.SandboxID)
		if err := checkFileRecord(current, ok, s.busy[record.SandboxID] || s.closed, target); err != nil {
			return err
		}
		if current.BootID != record.BootID || current.GuestEpoch != record.GuestEpoch {
			return status.Error(codes.Aborted, "sandbox boot changed")
		}
		return nil
	}}, nil
}
func (s *LimaServer) fileGuest(_ context.Context, target *nodev1.SandboxTarget) (fileGuest, error) {
	s.applyMu.Lock()
	record, ok := s.journal.getSandbox(target.GetSandboxId())
	err := checkFileRecord(record, ok, s.inFlight[target.GetSandboxId()] != "", target)
	s.applyMu.Unlock()
	if err != nil {
		return fileGuest{}, err
	}
	guest, identity, err := s.guestClient(record)
	if err != nil {
		return fileGuest{}, err
	}
	return fileGuest{client: guest.client, identity: identity, close: guest.close, check: func() error {
		s.applyMu.Lock()
		defer s.applyMu.Unlock()
		current, ok := s.journal.getSandbox(record.SandboxID)
		if err := checkFileRecord(current, ok, s.inFlight[record.SandboxID] != "", target); err != nil {
			return err
		}
		if current.BootID != record.BootID || current.GuestEpoch != record.GuestEpoch {
			return status.Error(codes.Aborted, "sandbox boot changed")
		}
		return nil
	}}, nil
}
func (b guestFileBackend) Write(parent context.Context, header *nodev1.RuntimeWriteHeader, source io.Reader) (*guestv1.WriteFileResponse, error) {
	if header == nil || header.SizeBytes > sandboxfiles.MaxBytes || len(header.Sha256) != sha256.Size || header.RequestId == "" {
		return nil, status.Error(codes.InvalidArgument, "file size, SHA256 and request ID are required")
	}
	if err := sandboxfiles.ValidatePath(header.Path, true); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	guest, err := b.open(ctx, header.Target)
	if err != nil {
		return nil, err
	}
	defer guest.close()
	stream, err := guest.client.WriteFile(ctx)
	if err != nil {
		return nil, err
	}
	send := func(frame *guestv1.WriteFileFrame) error {
		if err := stream.Send(frame); err != nil {
			if _, replyErr := stream.CloseAndRecv(); replyErr != nil {
				return replyErr
			}
			return err
		}
		return nil
	}
	if err := send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Header{Header: &guestv1.WriteFileHeader{Identity: guest.identity, RequestId: header.RequestId, Path: header.Path, SizeBytes: header.SizeBytes, Sha256: bytes.Clone(header.Sha256), Overwrite: header.Overwrite}}}); err != nil {
		return nil, err
	}
	buffer := make([]byte, sandboxfiles.ChunkBytes)
	var offset uint64
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			if offset+uint64(n) > header.SizeBytes {
				return nil, status.Error(codes.InvalidArgument, "file body exceeds declared size")
			}
			if err := send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Chunk{Chunk: &guestv1.FileChunk{Offset: offset, Data: bytes.Clone(buffer[:n])}}}); err != nil {
				return nil, err
			}
			offset += uint64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if offset != header.SizeBytes {
		return nil, status.Error(codes.InvalidArgument, "file body is shorter than declared size")
	}
	result, err := stream.CloseAndRecv()
	if err != nil {
		if status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded || status.Code(err) == codes.Canceled {
			return nil, status.Error(codes.Unknown, "COMMIT_UNKNOWN: verify the target file before retrying")
		}
		return nil, err
	}
	if err := guest.check(); err != nil {
		return nil, err
	}
	return result, nil
}
func (b guestFileBackend) Read(ctx context.Context, request *nodev1.RuntimeReadRequest, consume func(*guestv1.ReadFileFrame) error) error {
	if err := sandboxfiles.ValidatePath(request.GetPath(), false); err != nil {
		return err
	}
	guest, err := b.open(ctx, request.GetTarget())
	if err != nil {
		return err
	}
	defer guest.close()
	stream, err := guest.client.ReadFile(ctx, &guestv1.ReadFileRequest{Identity: guest.identity, Path: request.Path})
	if err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if frame.GetEnd() != nil {
			if err := guest.check(); err != nil {
				return err
			}
		}
		if err := consume(frame); err != nil {
			return err
		}
	}
}
func (b guestFileBackend) Mkdir(ctx context.Context, request *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error) {
	if err := sandboxfiles.ValidatePath(request.GetPath(), true); err != nil {
		return nil, err
	}
	guest, err := b.open(ctx, request.GetTarget())
	if err != nil {
		return nil, err
	}
	defer guest.close()
	result, err := guest.client.MakeDirectory(ctx, &guestv1.MakeDirectoryRequest{Identity: guest.identity, RequestId: request.RequestId, Path: request.Path, Parents: request.Parents})
	if err != nil {
		return nil, err
	}
	if err := guest.check(); err != nil {
		return nil, err
	}
	return result, nil
}

type runtimeFrameReader struct {
	stream       nodev1.SandboxRuntime_WriteFileServer
	pending      []byte
	offset, size uint64
}

func (r *runtimeFrameReader) Read(output []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(output, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	frame, err := r.stream.Recv()
	if err != nil {
		return 0, err
	}
	chunk := frame.GetChunk()
	if chunk == nil || chunk.Offset != r.offset || len(chunk.Data) == 0 || len(chunk.Data) > sandboxfiles.ChunkBytes || r.offset+uint64(len(chunk.Data)) > r.size {
		return 0, status.Error(codes.InvalidArgument, "invalid file chunk")
	}
	r.pending = chunk.Data
	r.offset += uint64(len(chunk.Data))
	return r.Read(output)
}
func writeRuntimeFile(backend sandboxfiles.Backend, stream nodev1.SandboxRuntime_WriteFileServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	header := first.GetHeader()
	if header == nil {
		return status.Error(codes.InvalidArgument, "first file frame must be a header")
	}
	result, err := backend.Write(stream.Context(), header, &runtimeFrameReader{stream: stream, size: header.SizeBytes})
	if err != nil {
		return err
	}
	return stream.SendAndClose(result)
}
func (s *CHVServer) WriteFile(stream nodev1.SandboxRuntime_WriteFileServer) error {
	return writeRuntimeFile(s.FileBackend(), stream)
}
func (s *LimaServer) WriteFile(stream nodev1.SandboxRuntime_WriteFileServer) error {
	return writeRuntimeFile(s.FileBackend(), stream)
}
func (s *CHVServer) ReadFile(request *nodev1.RuntimeReadRequest, stream nodev1.SandboxRuntime_ReadFileServer) error {
	return s.FileBackend().Read(stream.Context(), request, stream.Send)
}
func (s *LimaServer) ReadFile(request *nodev1.RuntimeReadRequest, stream nodev1.SandboxRuntime_ReadFileServer) error {
	return s.FileBackend().Read(stream.Context(), request, stream.Send)
}
func (s *CHVServer) MakeDirectory(ctx context.Context, request *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error) {
	return s.FileBackend().Mkdir(ctx, request)
}
func (s *LimaServer) MakeDirectory(ctx context.Context, request *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error) {
	return s.FileBackend().Mkdir(ctx, request)
}
