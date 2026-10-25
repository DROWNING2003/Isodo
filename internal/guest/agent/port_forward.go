package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validatePortForwardOpen(request *guestv1.PortForwardOpen) error {
	if request == nil || strings.TrimSpace(request.GetRequestId()) == "" {
		return status.Error(codes.InvalidArgument, "port forward request ID is required")
	}
	if request.GetPort() == 0 || request.GetPort() > 65535 {
		return status.Error(codes.InvalidArgument, "port forward port must be between 1 and 65535")
	}
	if strings.TrimSpace(request.GetService()) == "" || len(request.GetService()) > 128 {
		return status.Error(codes.InvalidArgument, "port forward service is required")
	}
	return nil
}

// PortForward connects one Guest-local TCP service to a bidirectional stream.
// Endpoint authorization is applied by the Node/Gateway; the Guest validates
// only the runtime identity and the shape of the requested service target.
func (s *Server) PortForward(stream guestv1.GuestService_PortForwardServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	if err := s.checkIdentity(open.GetIdentity()); err != nil {
		return err
	}
	if err := validatePortForwardOpen(open); err != nil {
		return err
	}
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", open.GetPort()), 5e9)
	if err != nil {
		return status.Errorf(codes.NotFound, "connect %s:%d: %v", open.GetService(), open.GetPort(), err)
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	fromGuest := make(chan error, 1)
	go func() {
		fromGuest <- forwardGuestToTCP(ctx, stream, connection)
	}()

	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return waitForward(ctx, fromGuest)
		}
		if recvErr != nil {
			return recvErr
		}
		switch {
		case frame.GetData() != nil:
			if _, err := connection.Write(frame.GetData().GetData()); err != nil {
				return status.Errorf(codes.Unavailable, "write forwarded T3 data: %v", err)
			}
		case frame.GetEnd() != nil:
			if frame.GetEnd().GetWrite() {
				if tcp, ok := connection.(interface{ CloseWrite() error }); ok {
					if err := tcp.CloseWrite(); err != nil {
						return status.Errorf(codes.Unavailable, "close forwarded T3 write side: %v", err)
					}
				}
			}
		default:
			return status.Error(codes.InvalidArgument, "unexpected port forward frame")
		}
	}
}

func forwardGuestToTCP(ctx context.Context, stream guestv1.GuestService_PortForwardServer, connection net.Conn) error {
	buffer := make([]byte, 32*1024)
	for {
		read, err := connection.Read(buffer)
		if read > 0 {
			if sendErr := stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_Data{Data: &guestv1.PortForwardData{Data: append([]byte(nil), buffer[:read]...)}}}); sendErr != nil {
				return sendErr
			}
		}
		if errors.Is(err, io.EOF) {
			return stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_End{End: &guestv1.PortForwardEnd{}}})
		}
		if err != nil {
			return status.Errorf(codes.Unavailable, "read forwarded T3 data: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func waitForward(ctx context.Context, result <-chan error) error {
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
