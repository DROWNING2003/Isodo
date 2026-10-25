package orchestrator

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type relayTestServer struct {
	nodev1.UnimplementedNodeOrchestratorServer
	open *nodev1.RelayOpen
}

func (s *relayTestServer) RelayPort(stream grpc.BidiStreamingServer[nodev1.RelayFrame, nodev1.RelayFrame]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	s.open = first.GetOpen()
	if err := stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_Data{Data: []byte("relay-ok")}}); err != nil {
		return err
	}
	return stream.Send(&nodev1.RelayFrame{Frame: &nodev1.RelayFrame_CloseWrite{CloseWrite: true}})
}

func TestRelayPortStreamsGuestData(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fake := &relayTestServer{}
	nodev1.RegisterNodeOrchestratorServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := &Client{service: nodev1.NewNodeOrchestratorClient(connection)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := client.RelayPort(ctx, "f323e70d-cd11-4a02-a245-6b4ed2ce1e31", 1, 8080, "t3")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buffer := make([]byte, 32)
	n, err := conn.Read(buffer)
	if err != nil || string(buffer[:n]) != "relay-ok" {
		t.Fatalf("read = %q, %v", buffer[:n], err)
	}
	if _, err = conn.Read(buffer); err != io.EOF {
		t.Fatalf("second read = %v, want EOF", err)
	}
	if fake.open == nil || fake.open.GetPort() != 8080 || fake.open.GetSandboxId() != "f323e70d-cd11-4a02-a245-6b4ed2ce1e31" {
		t.Fatalf("open = %+v", fake.open)
	}
}
