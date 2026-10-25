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
)

// RelayPort opens a long-lived Control-to-Node stream for one Guest TCP port.
// The API's Preview Gateway uses the returned net.Conn as a reverse-proxy
// upstream, so the API and Node may live on different hosts.
func (c *Client) RelayPort(ctx context.Context, sandboxID string, generation uint64, port uint16, service string) (net.Conn, error) {
	if err := c.requireService(); err != nil {
		return nil, err
	}
	if sandboxID == "" || port == 0 {
		return nil, errors.New("sandbox ID and port are required")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.service.RelayPort(streamCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open node relay: %w", err)
	}
	if err = stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_Open{Open: &nodev1.RelayOpen{
		SandboxId: sandboxID, OwnershipGeneration: generation, Port: uint32(port), Service: service,
	}}}); err != nil {
		cancel()
		return nil, fmt.Errorf("open node relay target: %w", err)
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
		_ = c.CloseWrite()
		_ = c.stream.CloseSend()
		c.cancel()
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
