package agent

import (
	"testing"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
)

func TestValidatePortForwardOpenAcceptsGuestServices(t *testing.T) {
	identity := &guestv1.RuntimeIdentity{SandboxId: "sbx", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	for name, request := range map[string]*guestv1.PortForwardOpen{
		"t3":          {Identity: identity, RequestId: "req", Service: "t3", Port: 8080},
		"guest agent": {Identity: identity, RequestId: "req", Service: "guest-agent", Port: 1024},
		"custom app":  {Identity: identity, RequestId: "req", Service: "custom", Port: 3000},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePortForwardOpen(request); err != nil {
				t.Fatalf("validatePortForwardOpen() error = %v", err)
			}
		})
	}
}
