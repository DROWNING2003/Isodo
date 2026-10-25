package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc"
)

// appRelay accepts loopback HTTP/WebSocket connections and bridges each one to
// one Guest TCP port. It is intentionally loopback-only: the public entry point
// must be the Control/Preview Gateway, never a Node listener.
type appRelay struct {
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}

	mu          sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
}

type portDialFunc func(context.Context, uint16) (net.Conn, error)

func startAppRelay(parent context.Context, dial portDialFunc) (*appRelay, error) {
	if dial == nil {
		return nil, errors.New("port dialer is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for app relay: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	relay := &appRelay{listener: listener, cancel: cancel, done: make(chan struct{}), connections: make(map[net.Conn]struct{})}
	go relay.serve(ctx, dial)
	return relay, nil
}

func (r *appRelay) URL() string {
	return "http://" + r.listener.Addr().String()
}

func (r *appRelay) Addr() string {
	return r.listener.Addr().String()
}

func (r *appRelay) Close() {
	if r == nil {
		return
	}
	r.cancel()
	_ = r.listener.Close()
	r.mu.Lock()
	r.closed = true
	for connection := range r.connections {
		_ = connection.Close()
	}
	r.connections = nil
	r.mu.Unlock()
	<-r.done
}

func (r *appRelay) track(connection net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.connections[connection] = struct{}{}
	return true
}

func (r *appRelay) untrack(connection net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.connections != nil {
		delete(r.connections, connection)
	}
}

func (r *appRelay) serve(ctx context.Context, dial portDialFunc) {
	defer close(r.done)
	for {
		client, err := r.listener.Accept()
		if err != nil {
			return
		}
		if !r.track(client) {
			_ = client.Close()
			continue
		}
		go func() {
			guest, err := dial(ctx, t3GuestPort)
			if err != nil {
				r.untrack(client)
				_ = client.Close()
				return
			}
			if !r.track(guest) {
				r.untrack(client)
				_ = client.Close()
				_ = guest.Close()
				return
			}
			bridge(client, guest)
			r.untrack(client)
			r.untrack(guest)
		}()
	}
}

func bridge(client, guest net.Conn) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = guest.Close()
		})
	}
	go func() {
		_, _ = io.Copy(guest, client)
		closeBoth()
	}()
	_, _ = io.Copy(client, guest)
	closeBoth()
}

// portForwardConn adapts the Guest PortForward bidi stream to net.Conn.
type portForwardConn struct {
	stream   guestv1.GuestService_PortForwardClient
	grpcConn *grpc.ClientConn
	cancel   context.CancelFunc

	readMu  sync.Mutex
	writeMu sync.Mutex
	buffer  []byte
	closed  sync.Once
}

func (c *portForwardConn) Read(destination []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.buffer) == 0 {
		frame, err := c.stream.Recv()
		if err != nil {
			return 0, err
		}
		switch {
		case frame.GetData() != nil:
			c.buffer = frame.GetData().GetData()
		case frame.GetEnd() != nil:
			return 0, io.EOF
		case frame.GetError() != nil:
			return 0, fmt.Errorf("%s: %s", frame.GetError().GetCode(), frame.GetError().GetMessage())
		default:
			return 0, errors.New("unexpected port forward frame")
		}
	}
	written := copy(destination, c.buffer)
	c.buffer = c.buffer[written:]
	return written, nil
}

func (c *portForwardConn) Write(payload []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_Data{Data: &guestv1.PortForwardData{Data: append([]byte(nil), payload...)}}}); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *portForwardConn) CloseWrite() error {
	return c.stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_End{End: &guestv1.PortForwardEnd{Write: true}}})
}

func (c *portForwardConn) Close() error {
	c.closed.Do(func() {
		_ = c.stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_End{End: &guestv1.PortForwardEnd{Write: true}}})
		if c.cancel != nil {
			c.cancel()
		}
		if c.grpcConn != nil {
			_ = c.grpcConn.Close()
		}
	})
	return nil
}

func (c *portForwardConn) LocalAddr() net.Addr              { return relayAddr("isodo-relay") }
func (c *portForwardConn) RemoteAddr() net.Addr             { return relayAddr("guest") }
func (c *portForwardConn) SetDeadline(time.Time) error      { return nil }
func (c *portForwardConn) SetReadDeadline(time.Time) error  { return nil }
func (c *portForwardConn) SetWriteDeadline(time.Time) error { return nil }

type relayAddr string

func (a relayAddr) Network() string { return "isodo-relay" }
func (a relayAddr) String() string  { return string(a) }
