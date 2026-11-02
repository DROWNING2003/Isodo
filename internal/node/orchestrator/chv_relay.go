package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

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
	guest, err := s.relayPortTarget(stream.Context(), open)
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
	if open.Service == "http-port" {
		if err := stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_Open{Open: open}}); err != nil {
			return err
		}
	}
	if open.Service == "http-port" {
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-stream.Context().Done():
					guest.Close()
					return
				case <-ticker.C:
					if err := s.validateHTTPPort(stream.Context(), open); err != nil {
						guest.Close()
						return
					}
				}
			}
		}()
	}

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

	type received struct {
		frame *nodev1.RelayFrame
		err   error
	}
	incoming := make(chan received)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			frame, err := stream.Recv()
			select {
			case incoming <- received{frame, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err := <-guestToStream:
			return err
		case result := <-incoming:
			if errors.Is(result.err, io.EOF) {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			switch frame := result.frame.Frame.(type) {
			case *nodev1.RelayFrame_Data:
				if _, err := guest.Write(frame.Data); err != nil {
					return err
				}
			case *nodev1.RelayFrame_CloseWrite:
				if !frame.CloseWrite {
					return status.Error(codes.InvalidArgument, "invalid close frame")
				}
				if c, ok := guest.(interface{ CloseWrite() error }); ok {
					_ = c.CloseWrite()
				}
			default:
				return status.Error(codes.InvalidArgument, "unexpected relay frame")
			}
		}
	}

}

// RelayPortConn opens the same Guest relay target without a gRPC stream. It is
// used by in-process validation and keeps the generation/liveness checks in
// one place with the gRPC RelayPort implementation.
func (s *CHVServer) RelayPortConn(ctx context.Context, sandboxID string, generation uint64, port uint16, service string) (net.Conn, error) {
	if service != "t3" || port != 8080 || !canonicalUUID(sandboxID) {
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

// Generic previews never fall back to the legacy workbench relay.
func (s *CHVServer) relayPortTarget(ctx context.Context, open *nodev1.RelayOpen) (net.Conn, error) {
	if open.Service == "t3" {
		return s.RelayPortConn(ctx, open.SandboxId, open.OwnershipGeneration, uint16(open.Port), open.Service)
	}
	if err := s.validateHTTPPort(ctx, open); err != nil {
		return nil, err
	}
	s.mu.Lock()
	instance := s.live[open.SandboxId]
	s.mu.Unlock()
	if instance == nil {
		return nil, errRelayNotFound
	}
	conn, err := s.dialGuestService(ctx, instance.VsockSocket(), instance.Identity(), uint16(open.Port), "http-port")
	if err != nil {
		return nil, err
	}
	if err := s.validateHTTPPort(ctx, open); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *CHVServer) validateHTTPPort(ctx context.Context, open *nodev1.RelayOpen) error {
	if open.Service != "http-port" || !canonicalUUID(open.SandboxId) || open.Port == 0 || open.Port > 65535 || open.Port == 1024 || open.Port == 8080 || open.OwnershipGeneration == 0 || open.Revision == 0 || open.BootId == "" || open.GuestEpoch == "" {
		return errRelayInvalid
	}
	s.mu.Lock()
	record, ok := s.journal.getSandbox(open.SandboxId)
	instance := s.live[open.SandboxId]
	busy := s.busy[open.SandboxId]
	s.mu.Unlock()
	if !ok || instance == nil {
		return errRelayNotFound
	}
	if busy || record.State != "running" || record.Generation != open.OwnershipGeneration || record.Revision != open.Revision || record.BootID != open.BootId || record.GuestEpoch != open.GuestEpoch {
		return errRelayGeneration
	}
	identity := instance.Identity()
	if identity == nil || identity.SandboxId != open.SandboxId || identity.OwnershipGeneration != open.OwnershipGeneration || identity.BootId != open.BootId || identity.GuestEpoch != open.GuestEpoch {
		return errRelayGeneration
	}
	if running, err := instance.Running(ctx); err != nil || !running {
		return errRelayNotFound
	}
	return nil
}
