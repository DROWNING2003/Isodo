package vmm

import (
	"context"
	"errors"
	"fmt"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
)

const resumeAfterSnapshotTimeout = 10 * time.Second

// CaptureTemplateSnapshot saves a ready, unbound Guest as a local CHV snapshot.
// The source VM is resumed after the snapshot request so it can remain a template source.
func CaptureTemplateSnapshot(ctx context.Context, client *Client, guestSocket, destinationDir string) error {
	if client == nil {
		return errors.New("Cloud Hypervisor client is required")
	}
	if _, err := guesttransport.WaitTemplateReady(ctx, guestSocket); err != nil {
		return fmt.Errorf("wait for template Guest before snapshot: %w", err)
	}
	if err := client.Pause(ctx); err != nil {
		return fmt.Errorf("pause template VM: %w", err)
	}
	snapshotErr := client.Snapshot(ctx, destinationDir)
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumeAfterSnapshotTimeout)
	defer cancel()
	resumeErr := client.Resume(resumeCtx)
	if snapshotErr != nil {
		snapshotErr = fmt.Errorf("snapshot template VM: %w", snapshotErr)
	}
	if resumeErr != nil {
		resumeErr = fmt.Errorf("resume template VM after snapshot: %w", resumeErr)
	}
	return errors.Join(snapshotErr, resumeErr)
}

// RestoreTemplateGuest restores a template using OnDemand memory paging, binds a new
// Sandbox identity, and independently reads Ready before returning the Guest to callers.
// Disk overlays, network devices, VM process creation, and public routing remain the
// responsibility of the Node lifecycle that owns this VMM client.
func RestoreTemplateGuest(
	ctx context.Context,
	client *Client,
	snapshotDir string,
	guestSocket string,
	expected *guestv1.RuntimeIdentity,
) (*guestv1.ReadyResponse, error) {
	if client == nil {
		return nil, errors.New("Cloud Hypervisor client is required")
	}
	if expected == nil || expected.GetSandboxId() == "" || expected.GetOwnershipGeneration() == 0 || expected.GetBootId() == "" {
		return nil, errors.New("sandbox ID, positive ownership generation, and boot ID are required")
	}
	if err := client.Restore(ctx, snapshotDir, MemoryRestoreOnDemand); err != nil {
		return nil, fmt.Errorf("restore template VM with OnDemand memory: %w", err)
	}
	if err := client.Resume(ctx); err != nil {
		return nil, fmt.Errorf("resume restored template VM: %w", err)
	}
	if _, err := guesttransport.WaitTemplateReady(ctx, guestSocket); err != nil {
		return nil, fmt.Errorf("wait for restored template Guest: %w", err)
	}
	bound, err := guesttransport.BindRuntime(ctx, guestSocket, expected)
	if err != nil {
		return nil, err
	}
	ready, err := guesttransport.WaitReady(ctx, guestSocket, expected)
	if err != nil {
		return nil, fmt.Errorf("verify Guest Ready after identity binding: %w", err)
	}
	if ready.GetIdentity().GetGuestEpoch() != bound.GetIdentity().GetGuestEpoch() {
		return nil, errors.New("Guest identity changed between BindRuntime and Ready")
	}
	return ready, nil
}
