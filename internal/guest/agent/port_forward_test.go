package agent

import (
	"testing"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
)

func TestValidatePortForwardOpenAcceptsGuestServices(t *testing.T) {
	identity := &guestv1.RuntimeIdentity{SandboxId: "sbx", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	for name, request := range map[string]*guestv1.PortForwardOpen{
		"t3":          {Identity: identity, RequestId: "req", Service: "t3", Port: 38080},
		"guest agent": {Identity: identity, RequestId: "req", Service: "guest-agent", Port: 31024},
		"custom app":  {Identity: identity, RequestId: "req", Service: "custom", Port: 3000},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePortForwardOpen(request); err != nil {
				t.Fatalf("validatePortForwardOpen() error = %v", err)
			}
		})
	}
}

func TestHTTPPortForwardRejectsRuntimeServices(t *testing.T) {
	for _, port := range []uint32{1024, 1080, 8080, 31024, 31080, 38080} {
		if err := validatePortForwardOpen(&guestv1.PortForwardOpen{RequestId: "http", Service: "http-port", Port: port}); err == nil {
			t.Fatalf("runtime HTTP port %d accepted", port)
		}
	}
	if err := validatePortForwardOpen(&guestv1.PortForwardOpen{RequestId: "http", Service: "http-port", Port: 3000}); err != nil {
		t.Fatal(err)
	}
}
