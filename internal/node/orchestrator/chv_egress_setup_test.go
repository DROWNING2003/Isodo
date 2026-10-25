package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"golang.org/x/sync/singleflight"
)

func TestCHVEgressCanceledRequestDoesNotStartSetup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := &CHVServer{injections: &injection.Manager{}}
	if err := s.prepareCHVEgress(ctx, sandboxRecord{EgressAllowed: true}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request started initialization: %v", err)
	}
}

func TestCHVEgressSetupSharesSameBootAndSeparatesNewBoot(t *testing.T) {
	s := &CHVServer{ctx: t.Context()}
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	setup := func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
		}
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("setup has no deadline")
		}
		<-release
		return nil
	}
	first := s.startEgressSetup(identity, setup)
	<-started
	second := s.startEgressSetup(identity, setup)
	nextBoot := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "next-boot", GuestEpoch: "next-epoch"}
	third := s.startEgressSetup(nextBoot, setup)
	close(release)
	for _, done := range []<-chan singleflight.Result{first, second, third} {
		if result := <-done; result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("setup calls=%d; same boot must share setup and new boot must not", calls.Load())
	}
}

func TestCHVEgressSetupFailureCanRetry(t *testing.T) {
	s := &CHVServer{ctx: t.Context()}
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox", BootId: "boot"}
	want := errors.New("temporary setup failure")
	if result := <-s.startEgressSetup(identity, func(context.Context) error { return want }); !errors.Is(result.Err, want) {
		t.Fatalf("setup error=%v", result.Err)
	}
	if result := <-s.startEgressSetup(identity, func(context.Context) error { return nil }); result.Err != nil {
		t.Fatalf("retry retained failed setup: %v", result.Err)
	}
}

func TestCHVEgressSetupUsesNodeLifetime(t *testing.T) {
	lifetime, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := &CHVServer{ctx: lifetime}
	started := make(chan struct{})
	done := s.startEgressSetup(&guestv1.RuntimeIdentity{SandboxId: "sandbox", BootId: "boot"}, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	cancel()
	if result := <-done; !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("Node shutdown did not cancel setup: %v", result.Err)
	}
}
