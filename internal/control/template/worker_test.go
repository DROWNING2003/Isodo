package template

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

type testQueue struct {
	job                          *BuildJob
	renewError                   error
	completed, failed, validated atomic.Int32
	summary                      string
}

func (q *testQueue) ClaimTemplateBuild(context.Context, time.Duration) (*BuildJob, error) {
	return q.job, nil
}
func (q *testQueue) RenewTemplateBuild(context.Context, BuildJob, time.Duration) error {
	return q.renewError
}
func (q *testQueue) ValidateTemplateBuild(context.Context, BuildJob) error {
	q.validated.Add(1)
	return nil
}
func (q *testQueue) CompleteTemplateBuild(context.Context, BuildJob, string, string) error {
	q.completed.Add(1)
	return nil
}
func (q *testQueue) FailTemplateBuild(_ context.Context, _ BuildJob, summary string) error {
	q.failed.Add(1)
	q.summary = summary
	return nil
}

type builderFunc func(context.Context, BuildJob) (BuildResult, error)

func (f builderFunc) Build(c context.Context, j BuildJob) (BuildResult, error) { return f(c, j) }

type publisherFunc func(context.Context, string, string) error

func (f publisherFunc) Publish(c context.Context, p, d string) error { return f(c, p, d) }
func workerFixture(t *testing.T) BuildResult {
	t.Helper()
	directory := t.TempDir()
	content := []byte("x")
	sum := sha256.Sum256(content)
	fileHash := hex.EncodeToString(sum[:])
	for _, name := range []string{"kernel", "rootfs.raw"} {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// An integrity-only fixture; real Guest validation belongs to the CHV Builder.
	data := []byte(`{"schema_version":1,"name":"base","version":"test","arch":"amd64","vmm":{"kind":"cloud-hypervisor","tested_version":"v54.0"},"kernel":{"file":"kernel","size_bytes":1,"sha256":"HASH"},"rootfs":{"file":"rootfs.raw","size_bytes":1,"sha256":"HASH","format":"raw","filesystem":"ext4","partitioned":false},"workspace":{"filesystem":"ext4","minimum_size_bytes":4096},"guest_protocol":{"major":1,"minimum_minor":0},"guest_agent":{"version":"test","capabilities":["ready.v1"]},"workload_user":{"name":"isodo","uid":1000,"gid":1000},"minimum_memory_mib":128}`)
	data = []byte(strings.ReplaceAll(string(data), "HASH", fileHash))
	if !json.Valid(data) {
		t.Fatal("invalid fixture")
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if _, err := templateartifact.Verify(directory, digest); err != nil {
		t.Fatal(err)
	}
	return BuildResult{Directory: directory, Digest: digest}
}
func TestWorkerPublishesOnlyValidatedArtifacts(t *testing.T) {
	fixture := workerFixture(t)
	for _, tc := range []struct {
		name                 string
		invalid, uploadFails bool
		wantComplete         int32
	}{{"success", false, false, 1}, {"invalid artifact", true, false, 0}, {"upload failure", false, true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			queue := &testQueue{job: &BuildJob{ID: "build"}}
			var uploads atomic.Int32
			var cleaned atomic.Bool
			result := fixture
			if tc.invalid {
				result.Digest = strings.Repeat("0", 64)
			}
			result.Cleanup = func() { cleaned.Store(true) }
			w := Worker{Queue: queue, Bucket: "templates", Builder: builderFunc(func(context.Context, BuildJob) (BuildResult, error) { return result, nil }), Publisher: publisherFunc(func(context.Context, string, string) error {
				uploads.Add(1)
				if tc.uploadFails {
					return errors.New("SECRET registry diagnostic")
				}
				return nil
			})}
			worked, err := w.RunOnce(t.Context())
			if !worked {
				t.Fatal("build was not claimed")
			}
			if tc.wantComplete == 1 && err != nil {
				t.Fatal(err)
			}
			if tc.wantComplete == 0 && err == nil {
				t.Fatal("failed build reported success")
			}
			if queue.completed.Load() != tc.wantComplete || !cleaned.Load() {
				t.Fatal("publication or cleanup mismatch")
			}
			if tc.invalid && uploads.Load() != 0 {
				t.Fatal("unverified artifact uploaded")
			}
			if strings.Contains(queue.summary, "SECRET") {
				t.Fatal("internal diagnostic leaked to public failure summary")
			}
		})
	}
}
func TestWorkerCancelsBuilderOnLeaseLoss(t *testing.T) {
	q := &testQueue{job: &BuildJob{ID: "lease-lost"}, renewError: ErrLeaseLost}
	w := Worker{Queue: q, Bucket: "templates", LeaseDuration: 3 * time.Second, Builder: builderFunc(func(ctx context.Context, _ BuildJob) (BuildResult, error) {
		<-ctx.Done()
		return BuildResult{}, ctx.Err()
	}), Publisher: publisherFunc(func(context.Context, string, string) error { t.Error("publish after lease loss"); return nil })}
	worked, err := w.RunOnce(t.Context())
	if !worked || !errors.Is(err, ErrLeaseLost) || q.completed.Load() != 0 {
		t.Fatalf("lease loss not fenced: %v", err)
	}
}
func TestWorkerDeadlineCancelsBuilder(t *testing.T) {
	q := &testQueue{job: &BuildJob{ID: "timed-out"}}
	w := Worker{Queue: q, Bucket: "templates", BuildTimeout: 20 * time.Millisecond, Builder: builderFunc(func(ctx context.Context, _ BuildJob) (BuildResult, error) {
		<-ctx.Done()
		return BuildResult{}, ctx.Err()
	}), Publisher: publisherFunc(func(context.Context, string, string) error { t.Error("publish after timeout"); return nil })}
	_, err := w.RunOnce(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || q.failed.Load() != 1 {
		t.Fatalf("deadline not enforced: %v", err)
	}
}
