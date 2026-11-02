package orchestrator

import (
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestHTTPPortRelayRequiresCurrentRuntime(t *testing.T) {
	node, input := commandNodeForSocket("unused")
	open := &nodev1.RelayOpen{SandboxId: input.SandboxID, OwnershipGeneration: 7, Revision: 11, BootId: "boot", GuestEpoch: "epoch", Port: 3000, Service: "http-port"}
	if err := node.validateHTTPPort(t.Context(), open); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*nodev1.RelayOpen)
	}{
		{"generation zero", func(o *nodev1.RelayOpen) { o.OwnershipGeneration = 0 }},
		{"old generation", func(o *nodev1.RelayOpen) { o.OwnershipGeneration = 6 }},
		{"old revision", func(o *nodev1.RelayOpen) { o.Revision = 10 }},
		{"old boot", func(o *nodev1.RelayOpen) { o.BootId = "old" }},
		{"old epoch", func(o *nodev1.RelayOpen) { o.GuestEpoch = "old" }},
		{"management port", func(o *nodev1.RelayOpen) { o.Port = 1024 }},
		{"T3 port", func(o *nodev1.RelayOpen) { o.Port = 8080 }},
		{"invalid port", func(o *nodev1.RelayOpen) { o.Port = 65536 }},
		{"unknown service", func(o *nodev1.RelayOpen) { o.Service = "guest" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := proto.Clone(open).(*nodev1.RelayOpen)
			tc.mutate(copy)
			if err := node.validateHTTPPort(t.Context(), copy); err == nil {
				t.Fatal("invalid relay accepted")
			}
		})
	}
	node.busy[input.SandboxID] = true
	if err := node.validateHTTPPort(t.Context(), open); err == nil {
		t.Fatal("busy runtime accepted")
	}
	delete(node.busy, input.SandboxID)
	record := node.journal.sandboxes[input.SandboxID]
	record.State = "stopped"
	node.journal.sandboxes[input.SandboxID] = record
	if err := node.validateHTTPPort(t.Context(), open); err == nil {
		t.Fatal("stopped runtime accepted")
	}
	if _, err := node.RelayPortConn(t.Context(), input.SandboxID, 0, 3000, "t3"); err == nil {
		t.Fatal("legacy relay exposed generic port")
	}
}
