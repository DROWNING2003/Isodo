package egressproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestRelayPreservesBytesAndHalfClose(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		c, e := upstream.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		body, _ := io.ReadAll(c)
		_, _ = c.Write(append([]byte("reply:"), body...))
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, listener, func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Addr().String())
		})
	}()
	c, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = c.Write([]byte{0, 1, 255})
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(c)
	if err != nil || string(body) != "reply:\x00\x01\xff" {
		t.Fatalf("body=%q error=%v", body, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("serve=%v", err)
	}
}
func TestRelayCancellationClosesIdleConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attached := make(chan struct{})
	remote, peer := net.Pipe()
	defer peer.Close()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, listener, func(context.Context) (net.Conn, error) { close(attached); return remote, nil })
	}()
	c, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-attached
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop")
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("downstream remained open")
	}
	if _, err := peer.Write([]byte("late")); err == nil {
		t.Fatal("upstream remained open")
	}
}
