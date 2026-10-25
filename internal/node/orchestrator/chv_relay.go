package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errRelayInvalid    = errors.New("relay requires a canonical sandbox ID and port")
	errRelayNotFound   = errors.New("sandbox is not running")
	errRelayGeneration = errors.New("relay generation does not match the running sandbox")
)

// RelayPort bridges a Control API TCP connection to one Guest port over the
// existing mTLS Node stream. No Node-local public listener is created.
func (s *CHVServer) RelayPort(stream grpc.BidiStreamingServer[nodev1.RelayFrame, nodev1.RelayFrame]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	if open == nil || !canonicalUUID(open.GetSandboxId()) || open.GetPort() == 0 || open.GetPort() > 65535 {
		return status.Error(codes.InvalidArgument, "relay requires a canonical sandbox ID and port")
	}
	guest, err := s.RelayPortConn(stream.Context(), open.GetSandboxId(), open.GetOwnershipGeneration(), uint16(open.GetPort()), open.GetService())
	switch {
	case errors.Is(err, errRelayInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, errRelayNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, errRelayGeneration):
		return status.Error(codes.FailedPrecondition, err.Error())
	case err != nil:
		return status.Error(codes.Unavailable, "connect Guest relay target")
	}
	defer guest.Close()

	guestToStream := make(chan error, 1)
	go func() {
		buffer := make([]byte, 32<<10)
		for {
			n, readErr := guest.Read(buffer)
			if n > 0 {
				if sendErr := stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_Data{Data: append([]byte(nil), buffer[:n]...)}}); sendErr != nil {
					guestToStream <- sendErr
					return
				}
			}
			if readErr == nil {
				continue
			}
			if errors.Is(readErr, io.EOF) {
				_ = stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_CloseWrite{CloseWrite: true}})
				guestToStream <- nil
			} else {
				guestToStream <- readErr
			}
			return
		}
	}()

	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return nil
		}
		if recvErr != nil {
			return recvErr
		}
		if data := frame.GetData(); data != nil {
			if _, writeErr := guest.Write(data); writeErr != nil {
				return writeErr
			}
		}
		if frame.GetCloseWrite() {
			if closeWriter, ok := guest.(interface{ CloseWrite() error }); ok {
				_ = closeWriter.CloseWrite()
			}
		}
		select {
		case relayErr := <-guestToStream:
			if relayErr != nil {
				return relayErr
			}
			// Guest closed its write side; keep reading client frames until EOF.
		default:
		}
	}
}

// RelayPortConn opens the same Guest relay target without a gRPC stream. It is
// used by in-process validation and keeps the generation/liveness checks in
// one place with the gRPC RelayPort implementation.
func (s *CHVServer) RelayPortConn(ctx context.Context, sandboxID string, generation uint64, port uint16, service string) (net.Conn, error) {
	if !canonicalUUID(sandboxID) || port == 0 {
		return nil, errRelayInvalid
	}
	s.mu.Lock()
	instance := s.live[sandboxID]
	s.mu.Unlock()
	if instance == nil {
		return nil, errRelayNotFound
	}
	identity := instance.Identity()
	if identity == nil || (generation != 0 && generation != identity.OwnershipGeneration) {
		return nil, errRelayGeneration
	}
	return s.dialGuestPort(ctx, instance.VsockSocket(), identity, port)
}
