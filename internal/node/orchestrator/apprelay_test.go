package orchestrator

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAppRelayBridgesLoopbackHTTP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "t3-ready")
	}))
	defer backend.Close()
	backendAddress := backend.Listener.Addr().String()

	relay, err := startAppRelay(context.Background(), func(_ context.Context, port uint16) (net.Conn, error) {
		if port != t3GuestPort {
			t.Errorf("relay dialed port %d, want %d", port, t3GuestPort)
		}
		return net.Dial("tcp", backendAddress)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	response, err := http.Get(relay.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || string(body) != "t3-ready" {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}
}

func TestAppRelayCloseClosesActiveConnections(t *testing.T) {
	relay, err := startAppRelay(context.Background(), func(context.Context, uint16) (net.Conn, error) {
		guest, peer := net.Pipe()
		go func() {
			_, _ = io.Copy(io.Discard, peer)
			_ = peer.Close()
		}()
		return guest, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", relay.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("open")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		relay.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay Close blocked on an active connection")
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("active client connection remained open")
	}
	_ = client.Close()
}
