package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestCommandsNodeRejectsInvalidRequestsBeforeOpeningGuest(t *testing.T) {
	cases := []struct {
		name string
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
		key string
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
