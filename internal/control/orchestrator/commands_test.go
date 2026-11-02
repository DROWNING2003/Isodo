package orchestrator

import (
	"context"
	"net"
	"testing"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// An older Node without the new Commands RPC must return Unimplemented,
// allowing the HTTP handler to report 501 instead of opening Guest tunnels.
func TestCommandRunnerUnavailableOnOldNode(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	connection, err := grpc.NewClient("passthrough:///old-node",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := &Client{connection: connection, service: nodev1.NewNodeOrchestratorClient(connection)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.RunCommand(ctx, &nodev1.SandboxTarget{
		SandboxId: "f323e70d-cd11-4a02-a245-6b4ed2ce1e31",
		OwnershipGeneration: 1, Revision: 1,
	}, sandbox.CommandRequest{
		Argv: []string{"/bin/true"}, RequestID: "request-12345678", TimeoutSeconds: 30,
	})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("old Node response: code=%v, want Unimplemented (err=%v)", status.Code(err), err)
	}
}
