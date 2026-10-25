package orchestrator

import (
	"context"
	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

type t3StartCapture struct {
	guestv1.GuestServiceClient
	requests []*guestv1.StartProcessRequest
	expire   bool
}

func (c *t3StartCapture) StartProcess(_ context.Context, request *guestv1.StartProcessRequest, _ ...grpc.CallOption) (*guestv1.ProcessInfo, error) {
	c.requests = append(c.requests, request)
	if c.expire && len(c.requests) == 1 {
		return nil, status.Error(codes.FailedPrecondition, "EXECUTION_EXPIRED")
	}
	return &guestv1.ProcessInfo{}, nil
}
func TestT3SupervisorHasNoCommandDeadline(t *testing.T) {
	for _, expired := range []bool{false, true} {
		client := &t3StartCapture{expire: expired}
		_, err := startGuestProcessWithEnv(t.Context(), client, &guestv1.RuntimeIdentity{}, "start", []string{"/bin/sh", "-c", "while true; do sleep 1; done"}, map[string]string{"HOME": "/workspace/.home"})
		if err != nil {
			t.Fatal(err)
		}
		for _, request := range client.requests {
			if request.CommandTimeoutSeconds == nil || request.GetCommandTimeoutSeconds() != 0 {
				t.Fatal("T3 supervisor inherits the 60-second Guest command timeout")
			}
		}
	}
}
