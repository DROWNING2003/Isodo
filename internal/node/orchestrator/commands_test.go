package orchestrator

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestCommandsNodeRejectsInvalidRequestsBeforeOpeningGuest(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"missing envelope", nil},
		{"malformed JSON", []byte("{")},
		{"invalid UUID", mustMarshalCommand(t, runtimecommands.Request{
			SandboxID: "not-a-uuid", Generation: 1, Revision: 1,
			Key: "request-12345678", Command: runtimecommands.Command{Argv: []string{"ls"}, TimeoutSeconds: 60},
		})},
		{"invalid key", mustMarshalCommand(t, runtimecommands.Request{
			SandboxID: "f323e70d-cd11-4a02-a245-6b4ed2ce1e31", Generation: 1, Revision: 1,
			Key: "bad key", Command: runtimecommands.Command{Argv: []string{"ls"}, TimeoutSeconds: 60},
		})},
		{"excessive timeout", mustMarshalCommand(t, runtimecommands.Request{
			SandboxID: "f323e70d-cd11-4a02-a245-6b4ed2ce1e31", Generation: 1, Revision: 1,
			Key: "request-12345678", Command: runtimecommands.Command{Argv: []string{"ls"}, TimeoutSeconds: 301},
		})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Invalid requests are rejected before dereferencing CHV state.
			var node *CHVServer
			_, err := node.RunCommand(context.Background(), wrapperspb.Bytes(test.payload))
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code=%v, want InvalidArgument (err=%v)", status.Code(err), err)
			}
		})
	}
}

func mustMarshalCommand(t *testing.T, request runtimecommands.Request) []byte {
	t.Helper()
	b, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCommandKeyValidation(t *testing.T) {
	for _, input := range []struct {
		key   string
		valid bool
	}{
		{"request-12345678", true},
		{"only-8-!", false},
		{"too", false},
		{"a1234567", true},
		{"bad key with spaces", false},
		{"newline\nkey", false},
	} {
		if got := validCommandKey(input.key); got != input.valid {
			t.Errorf("%q: valid=%v, want %v", input.key, got, input.valid)
		}
	}
}

func TestGuestCommandRequestIDIsScopedToCurrentRuntime(t *testing.T) {
	id := "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"
	first := guestCommandRequestID(id, 2, "boot-1", "epoch-1", "client-request")
	if first != guestCommandRequestID(id, 2, "boot-1", "epoch-1", "client-request") {
		t.Fatal("same key and runtime must deduplicate")
	}
	if first == guestCommandRequestID(id, 2, "boot-2", "epoch-1", "client-request") ||
		first == guestCommandRequestID(id, 2, "boot-1", "epoch-2", "client-request") ||
		first == guestCommandRequestID(id, 3, "boot-1", "epoch-1", "client-request") ||
		first == guestCommandRequestID(id, 2, "boot-1", "epoch-1", "client-request-other") {
		t.Fatal("different key or runtime must not reuse execution ID")
	}
}

func TestCommandRPCIsASeparateSingleMethodService(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterCommandRunner(server, &CHVServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	conn, err := grpc.NewClient("passthrough:///node-command-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var reply wrapperspb.BytesValue
	err = conn.Invoke(ctx, commandRunMethod, wrapperspb.Bytes([]byte(`{"sandbox_id":"invalid"}`)), &reply)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Node Commands method was not registered: code=%v err=%v", status.Code(err), err)
	}
	// The old arbitrary Guest RPC tunnel is not part of this service's API.
	if methods := server.GetServiceInfo()[commandRunnerServiceDesc.ServiceName].Methods; len(methods) != 1 || methods[0].Name != "RunCommand" {
		t.Fatalf("unexpected Node Commands methods: %+v", methods)
	}
}

func TestCommandRuntimeFencesRevisionAndGuestIdentity(t *testing.T) {
	id := "f323e70d-cd11-4a02-a245-6b4ed2ce1e31"
	instance := &fakeCHV{identity: &guestv1.RuntimeIdentity{
		SandboxId: id, OwnershipGeneration: 7, BootId: "boot-7", GuestEpoch: "epoch-7",
	}}
	instance.running.Store(true)
	node := &CHVServer{
		journal: &limaJournal{sandboxes: map[string]sandboxRecord{
			id: {SandboxID: id, State: "running", Generation: 7, Revision: 11, BootID: "boot-7", GuestEpoch: "epoch-7"},
		}},
		live: map[string]CHVRuntime{id: instance},
		busy: map[string]bool{},
	}
	command := runtimecommands.Request{
		SandboxID: id, Generation: 7, Revision: 10, Key: "request-12345678",
		Command: runtimecommands.Command{Argv: []string{"/bin/true"}, TimeoutSeconds: 30},
	}
	_, err := node.RunCommand(context.Background(), wrapperspb.Bytes(mustMarshalCommand(t, command)))
	if status.Code(err) != codes.Aborted {
		t.Fatalf("stale revision: code=%v, want Aborted", status.Code(err))
	}
	command.Revision = 11
	instance.identity.GuestEpoch = "old-epoch"
	_, err = node.RunCommand(context.Background(), wrapperspb.Bytes(mustMarshalCommand(t, command)))
	if status.Code(err) != codes.Aborted {
		t.Fatalf("stale Guest identity: code=%v, want Aborted", status.Code(err))
	}
	instance.identity.GuestEpoch = "epoch-7"
	_, err = node.RunCommand(context.Background(), wrapperspb.Bytes(mustMarshalCommand(t, command)))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unavailable Guest transport: code=%v, want FailedPrecondition", status.Code(err))
	}
}
