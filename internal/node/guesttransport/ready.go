package guesttransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

const defaultRetryInterval = 250 * time.Millisecond
const maxVsockHandshakeReplyBytes = 64

func WaitReady(ctx context.Context, socketPath string, expected *guestv1.RuntimeIdentity) (*guestv1.ReadyResponse, error) {
	if socketPath == "" {
		return nil, errors.New("guest vsock socket path is required")
	}
	if expected == nil {
		return nil, errors.New("expected guest identity is required")
	}

	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("wait for guest readiness: %w (last attempt: %v)", ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("wait for guest readiness: %w", ctx.Err())
		case <-timer.C:
		}

		response, err := requestReady(ctx, socketPath)
		if err == nil {
			if err := validateReady(response, expected); err != nil {
				return nil, err
			}
			return response, nil
		}
		lastErr = err

		timer.Reset(defaultRetryInterval)
	}
}

// WaitReadyTCP verifies a Guest Agent reached through a loopback-only Lima port forward.
func WaitReadyTCP(ctx context.Context, address string, expected *guestv1.RuntimeIdentity) (*guestv1.ReadyResponse, error) {
	if expected == nil {
		return nil, errors.New("expected guest identity is required")
	}
	conn, err := NewTCPClientConn(address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return waitReady(ctx, guestv1.NewGuestServiceClient(conn).Ready, expected)
}

// WaitTemplateReadyTCP waits until an unbound template Guest Agent accepts RPCs.
func WaitTemplateReadyTCP(ctx context.Context, address string) (*guestv1.ReadyResponse, error) {
	conn, err := NewTCPClientConn(address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return waitTemplateReady(ctx, guestv1.NewGuestServiceClient(conn).Ready)
}

// WaitTemplateReady waits until an unbound template Guest Agent accepts RPCs.
func WaitTemplateReady(ctx context.Context, socketPath string) (*guestv1.ReadyResponse, error) {
	if socketPath == "" {
		return nil, errors.New("guest vsock socket path is required")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("wait for template Guest readiness: %w (last attempt: %v)", ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("wait for template Guest readiness: %w", ctx.Err())
		case <-timer.C:
		}
		response, err := requestReady(ctx, socketPath)
		if err == nil {
			if response == nil || response.ProtocolMajor != 1 {
				return nil, errors.New("template Guest returned an unsupported readiness response")
			}
			if response.RuntimeBound || response.Identity != nil {
				return nil, errors.New("template Guest is already bound to a runtime identity")
			}
			return response, nil
		}
		lastErr = err
		timer.Reset(defaultRetryInterval)
	}
}

// BindRuntime assigns a restored template Guest to one Node-owned Sandbox.
func BindRuntime(ctx context.Context, socketPath string, expected *guestv1.RuntimeIdentity) (*guestv1.ReadyResponse, error) {
	if socketPath == "" {
		return nil, errors.New("guest vsock socket path is required")
	}
	if expected == nil || expected.SandboxId == "" || expected.OwnershipGeneration == 0 || expected.BootId == "" {
		return nil, errors.New("sandbox ID, positive ownership generation, and boot ID are required")
	}
	response, err := requestBindRuntime(ctx, socketPath, expected)
	if err != nil {
		return nil, fmt.Errorf("bind template Guest runtime identity: %w", err)
	}
	if err := validateReady(response, expected); err != nil {
		return nil, err
	}
	return response, nil
}

// BindRuntimeTCP assigns a restored template Guest reached through a Lima port forward.
func BindRuntimeTCP(ctx context.Context, address string, expected *guestv1.RuntimeIdentity) (*guestv1.ReadyResponse, error) {
	if expected == nil || expected.SandboxId == "" || expected.OwnershipGeneration == 0 || expected.BootId == "" {
		return nil, errors.New("sandbox ID, positive ownership generation, and boot ID are required")
	}
	conn, err := NewTCPClientConn(address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	response, err := guestv1.NewGuestServiceClient(conn).BindRuntime(ctx, &guestv1.BindRuntimeRequest{
		SandboxId:           expected.GetSandboxId(),
		OwnershipGeneration: expected.GetOwnershipGeneration(),
		BootId:              expected.GetBootId(),
	})
	if err != nil {
		return nil, fmt.Errorf("bind template Guest runtime identity: %w", err)
	}
	if err := validateReady(response, expected); err != nil {
		return nil, err
	}
	return response, nil
}

func waitReady(ctx context.Context, ready func(context.Context, *emptypb.Empty, ...grpc.CallOption) (*guestv1.ReadyResponse, error), expected *guestv1.RuntimeIdentity) (*guestv1.ReadyResponse, error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("wait for guest readiness: %w (last attempt: %v)", ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("wait for guest readiness: %w", ctx.Err())
		case <-timer.C:
		}
		response, err := ready(ctx, new(emptypb.Empty))
		if err == nil {
			if err := validateReady(response, expected); err != nil {
				return nil, err
			}
			return response, nil
		}
		lastErr = err
		timer.Reset(defaultRetryInterval)
	}
}

func waitTemplateReady(ctx context.Context, ready func(context.Context, *emptypb.Empty, ...grpc.CallOption) (*guestv1.ReadyResponse, error)) (*guestv1.ReadyResponse, error) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("wait for template Guest readiness: %w (last attempt: %v)", ctx.Err(), lastErr)
			}
			return nil, fmt.Errorf("wait for template Guest readiness: %w", ctx.Err())
		case <-timer.C:
		}
		response, err := ready(ctx, new(emptypb.Empty))
		if err == nil {
			if response == nil || response.ProtocolMajor != 1 {
				return nil, errors.New("template Guest returned an unsupported readiness response")
			}
			if response.RuntimeBound || response.Identity != nil {
				return nil, errors.New("template Guest is already bound to a runtime identity")
			}
			return response, nil
		}
		lastErr = err
		timer.Reset(defaultRetryInterval)
	}
}

func requestReady(ctx context.Context, socketPath string) (*guestv1.ReadyResponse, error) {
	conn, err := NewVsockClientConn(socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return guestv1.NewGuestServiceClient(conn).Ready(ctx, &emptypb.Empty{})
}

func requestBindRuntime(ctx context.Context, socketPath string, expected *guestv1.RuntimeIdentity) (*guestv1.ReadyResponse, error) {
	conn, err := NewVsockClientConn(socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return guestv1.NewGuestServiceClient(conn).BindRuntime(ctx, &guestv1.BindRuntimeRequest{
		SandboxId:           expected.GetSandboxId(),
		OwnershipGeneration: expected.GetOwnershipGeneration(),
		BootId:              expected.GetBootId(),
	})
}

// NewVsockClientConn connects through a Node-private CHV Unix relay. Callers own Close.
func NewVsockClientConn(socketPath string) (*grpc.ClientConn, error) {
	if socketPath == "" {
		return nil, errors.New("guest vsock socket path is required")
	}
	return newClientConn(func(dialCtx context.Context) (net.Conn, error) { return dialVsock(dialCtx, socketPath) })
}

// NewTCPClientConn creates an insecure gRPC connection only to a loopback address.
// Lima's host port forward is expected to bind there; accepting non-loopback addresses
// would expose Guest management RPCs to the host network.
func NewTCPClientConn(address string) (*grpc.ClientConn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid guest TCP address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("guest TCP address must use a loopback IP")
	}
	return newClientConn(func(ctx context.Context) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, "tcp", address)
	})
}

func newClientConn(dial func(context.Context) (net.Conn, error)) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		"passthrough:///guest",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return dial(ctx) }),
	)
}

func dialVsock(ctx context.Context, socketPath string) (net.Conn, error) {
	// CHV can accept the relay socket before the Guest listener exists. Bound
	// that handshake independently of gRPC's 20-second connection deadline so
	// readiness retries can reach a listener that starts shortly afterwards.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := new(net.Dialer).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial guest vsock relay: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if _, err := io.WriteString(conn, "CONNECT 1024\n"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open guest vsock port: %w", err)
	}
	acknowledgement := make([]byte, 0, 32)
	var one [1]byte
	for range maxVsockHandshakeReplyBytes {
		n, err := conn.Read(one[:])
		if n > 0 {
			acknowledgement = append(acknowledgement, one[0])
			if one[0] == '\n' {
				break
			}
		}
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("read guest vsock acknowledgement: %w", err)
		}
	}
	if len(acknowledgement) == maxVsockHandshakeReplyBytes && acknowledgement[len(acknowledgement)-1] != '\n' {
		_ = conn.Close()
		return nil, errors.New("guest vsock acknowledgement exceeds the maximum length")
	}
	if !strings.HasPrefix(string(acknowledgement), "OK ") || !strings.HasSuffix(string(acknowledgement), "\n") {
		_ = conn.Close()
		return nil, fmt.Errorf("guest vsock relay rejected port 1024 (reply %q)", strings.TrimSpace(string(acknowledgement)))
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func validateReady(response *guestv1.ReadyResponse, expected *guestv1.RuntimeIdentity) error {
	if response == nil || response.Identity == nil || !response.RuntimeBound {
		return errors.New("guest returned an incomplete readiness response")
	}
	if response.ProtocolMajor != 1 {
		return fmt.Errorf("guest protocol major %d is unsupported", response.ProtocolMajor)
	}
	actual := response.Identity
	if actual.SandboxId != expected.SandboxId ||
		actual.OwnershipGeneration != expected.OwnershipGeneration ||
		actual.BootId != expected.BootId {
		return errors.New("guest readiness identity does not match the assigned sandbox execution")
	}
	if actual.GuestEpoch == "" {
		return errors.New("guest readiness response has no guest epoch")
	}
	if expected.GuestEpoch != "" && actual.GuestEpoch != expected.GuestEpoch {
		return errors.New("guest readiness epoch changed within the assigned execution")
	}
	return nil
}
