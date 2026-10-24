package template

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

var ErrLeaseLost = errors.New("template build lease lost")

type BuildJob struct {
	ID, TemplateID, TenantID, LeaseToken     string
	SourceDigest, SourceBucket, SourcePrefix string
	Recipe                                   api.BuildRecipe
	PublishTag                               string
	ExpectedTagBuildID                       string
}
type BuildResult struct {
	Directory, Digest string
	Cleanup           func()
}
type Builder interface {
	Build(context.Context, BuildJob) (BuildResult, error)
}
type BuildPublisher interface {
	Publish(context.Context, string, string) error
}
type BuildQueue interface {
	ClaimTemplateBuild(context.Context, time.Duration) (*BuildJob, error)
	RenewTemplateBuild(context.Context, BuildJob, time.Duration) error
	ValidateTemplateBuild(context.Context, BuildJob) error
	CompleteTemplateBuild(context.Context, BuildJob, string, string) error
	FailTemplateBuild(context.Context, BuildJob, string) error
}
type Worker struct {
	Queue                       BuildQueue
	Builder                     Builder
	Publisher                   BuildPublisher
	Bucket                      string
	LeaseDuration, BuildTimeout time.Duration
	Logger                      *slog.Logger
}

// RunOnce owns one durable build until completion or lease loss. Guest/VMM cleanup
// is synchronous with Builder return, including failure and context cancellation.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w.Queue == nil || w.Builder == nil || w.Publisher == nil || w.Bucket == "" {
		return false, errors.New("template Worker dependencies are required")
	}
	lease := w.LeaseDuration
	if lease == 0 {
		lease = 30 * time.Second
	}
	if lease < 3*time.Second || lease > 5*time.Minute {
		return false, errors.New("build lease must be between 3 seconds and 5 minutes")
	}
	if w.BuildTimeout < 0 {
		return false, errors.New("build timeout must not be negative")
	}
	job, err := w.Queue.ClaimTemplateBuild(ctx, lease)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	timeout := w.BuildTimeout
	if timeout == 0 {
		timeout = 15 * time.Minute
	}
	buildCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	leaseCtx, stopLease := context.WithCancel(buildCtx)
	defer stopLease()
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				renewal, cancelRenewal := context.WithTimeout(leaseCtx, lease/3)
				err := w.Queue.RenewTemplateBuild(renewal, *job, lease)
				cancelRenewal()
				if err != nil {
					cancel()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	stage := "preparation"
	result, buildErr := w.Builder.Build(buildCtx, *job)
	if result.Cleanup != nil {
		defer result.Cleanup()
	}
	if buildErr == nil {
		stage = "validation"
		buildErr = w.Queue.ValidateTemplateBuild(buildCtx, *job)
	}
	if buildErr == nil {
		_, buildErr = templateartifact.Verify(result.Directory, result.Digest)
	}
	if buildErr == nil {
		stage = "artifact upload"
		buildErr = w.Publisher.Publish(buildCtx, result.Directory, result.Digest)
	}
	stopLease()
	leaseErr := <-heartbeatDone
	if buildErr == nil && leaseErr == nil {
		stage = "publication"
		publicationCtx, publicationCancel := context.WithTimeout(buildCtx, lease/3)
		buildErr = w.Queue.CompleteTemplateBuild(publicationCtx, *job, result.Digest, w.Bucket)
		publicationCancel()
	}
	if buildErr == nil && leaseErr == nil {
		return true, nil
	}
	// Failure summaries are stage-only: internal paths and registry credentials
	// in a Builder/SDK error must never become public build metadata.
	failureCtx, failureCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer failureCancel()
	failureErr := w.Queue.FailTemplateBuild(failureCtx, *job, "Build failed during "+stage+".")
	if errors.Is(failureErr, ErrLeaseLost) {
		failureErr = nil
	}
	return true, fmt.Errorf("build %s (%s): %w", job.ID, stage, errors.Join(buildErr, leaseErr, failureErr))
}

func (w *Worker) Run(ctx context.Context) error {
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		worked, err := w.RunOnce(ctx)
		if err != nil {
			logger.Error("template build iteration failed", "error", err)
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
