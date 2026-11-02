package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// RelayPort opens a long-lived Control-to-Node stream for one Guest TCP port.
// The API's Preview Gateway uses the returned net.Conn as a reverse-proxy
// upstream, so the API and Node may live on different hosts.
func (c *Client) RelayPort(ctx context.Context, sandboxID string, generation uint64, port uint16, service string) (net.Conn, error) {
	return c.relayPort(ctx, &nodev1.RelayOpen{SandboxId: sandboxID, OwnershipGeneration: generation, Port: uint32(port), Service: service})
}

// RelayHTTPPort requires a fully fenced target; the Node validates it again.
func (c *Client) RelayHTTPPort(ctx context.Context, open *nodev1.RelayOpen) (net.Conn, error) {
	if open == nil || open.Service != "http-port" || open.Revision == 0 || open.OwnershipGeneration == 0 || open.BootId == "" || open.GuestEpoch == "" {
		return nil, errors.New("invalid HTTP preview runtime")
	}
	return c.relayPort(ctx, open)
}

func (c *Client) relayPort(ctx context.Context, open *nodev1.RelayOpen) (net.Conn, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	if open.SandboxId == "" || open.Port == 0 {
		return nil, errors.New("sandbox ID and port are required")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.service.RelayPort(streamCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open node relay: %w", err)
	}
	if err = stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_Open{Open: open}}); err != nil {
		cancel()
		return nil, fmt.Errorf("open node relay target: %w", err)
	}
	if open.Service == "http-port" {
		// Older Nodes ignored service/fencing fields. Require an explicit echo
		// after validation before sending any HTTP bytes to the Guest.
		type acknowledgement struct {
			frame *nodev1.RelayFrame
			err   error
		}
		ready := make(chan acknowledgement, 1)
		go func() { frame, err := stream.Recv(); ready <- acknowledgement{frame, err} }()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			cancel()
			return nil, ctx.Err()
		case <-timer.C:
			cancel()
			return nil, errors.New("Node does not acknowledge fenced HTTP previews")
		case result := <-ready:
			if result.err != nil {
				cancel()
				return nil, fmt.Errorf("authorize Node HTTP relay: %w", result.err)
			}
			if result.frame.GetOpen() == nil || !proto.Equal(result.frame.GetOpen(), open) {
				cancel()
				return nil, errors.New("Node returned an invalid HTTP relay acknowledgement")
			}
		}
	}
	return &relayConn{stream: stream, cancel: cancel}, nil
}

type relayConn struct {
	stream grpc.BidiStreamingClient[nodev1.RelayFrame, nodev1.RelayFrame]
	cancel context.CancelFunc

	readMu  sync.Mutex
	writeMu sync.Mutex
	buffer  []byte
	closed  sync.Once
}

func (c *relayConn) Read(destination []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.buffer) == 0 {
		frame, err := c.stream.Recv()
		if err != nil {
			return 0, err
		}
		switch {
		case frame.GetData() != nil:
			c.buffer = frame.GetData()
		case frame.GetCloseWrite():
			return 0, io.EOF
		case frame.GetOpen() != nil:
			return 0, errors.New("unexpected relay open frame")
		default:
			return 0, errors.New("unexpected relay frame")
		}
	}
	written := copy(destination, c.buffer)
	c.buffer = c.buffer[written:]
	return written, nil
}

func (c *relayConn) Write(payload []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_Data{Data: append([]byte(nil), payload...)}}); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *relayConn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_CloseWrite{CloseWrite: true}})
}

func (c *relayConn) Close() error {
	c.closed.Do(func() {
		// Cancel first: a blocked writer must not prevent lifecycle revocation.
		c.cancel()
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		_ = c.stream.CloseSend()
	})
	return nil
}

func (c *relayConn) LocalAddr() net.Addr              { return relayAddr("isodo-control") }
func (c *relayConn) RemoteAddr() net.Addr             { return relayAddr("isodo-node") }
func (c *relayConn) SetDeadline(time.Time) error      { return nil }
func (c *relayConn) SetReadDeadline(time.Time) error  { return nil }
func (c *relayConn) SetWriteDeadline(time.Time) error { return nil }

type relayAddr string

func (a relayAddr) Network() string { return "isodo-relay" }
func (a relayAddr) String() string  { return string(a) }
