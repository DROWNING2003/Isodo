package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/google/uuid"
)

func TestTemplateBuildLeaseAndTagPublication(t *testing.T) {
	url := os.Getenv("ISODO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	ctx := t.Context()
	s, err := New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tenant, err := s.ResolveTenant(ctx, fmt.Sprintf("https://worker-%d.test", time.Now().UnixNano()), "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateTemplate(ctx, tenant, api.CreateTemplate{Name: "worker-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanup, `DELETE FROM template_tags WHERE template_id=$1::uuid`, item.ID)
		_, _ = s.pool.Exec(cleanup, `DELETE FROM template_builds WHERE template_id=$1::uuid`, item.ID)
		_, _ = s.pool.Exec(cleanup, `DELETE FROM templates WHERE id=$1::uuid`, item.ID)
		cleanupTestTenants(t, url, tenant)
	})
	// Ready source fixtures exercise DB fencing; no VM or S3 readiness is implied.
	ready := func(digest string) string {
		t.Helper()
		var id string
		err := s.pool.QueryRow(ctx, `INSERT INTO template_builds(template_id,status,manifest_digest,artifact_bucket,artifact_prefix) VALUES($1::uuid,'ready',$2,'isodo-templates',$3) RETURNING id::text`, item.ID, digest, "templates/sha256/"+digest).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	source := ready(strings.Repeat("a", 64))
	replacement := ready(strings.Repeat("b", 64))
	if _, err = s.SetTemplateTag(ctx, tenant, item.ID, "default", api.SetTemplateTag{BuildID: source}); err != nil {
		t.Fatal(err)
	}
	enqueue := func() api.TemplateBuild {
		t.Helper()
		b, err := s.CreateTemplateBuild(ctx, tenant, item.ID, api.BuildRecipe{SourceBuildID: source})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first, second := enqueue(), enqueue()
	jobs := make(chan *template.BuildJob, 8)
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			job, err := s.ClaimTemplateBuild(ctx, 30*time.Second)
			if err != nil {
				failures <- err
			}
			if job != nil {
				jobs <- job
			}
		})
	}
	workers.Wait()
	close(jobs)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one active build for a template, got %d", len(jobs))
	}
	job := <-jobs
	if job.ID != first.ID {
		t.Fatalf("queue order: %s != %s", job.ID, first.ID)
	}
	if err = s.DeleteTemplate(ctx, tenant, item.ID); !errors.Is(err, template.ErrConflict) {
		t.Fatalf("deleted active build template: %v", err)
	}
	stale := *job
	stale.LeaseToken = uuid.NewString()
	if err = s.RenewTemplateBuild(ctx, stale, 30*time.Second); !errors.Is(err, template.ErrLeaseLost) {
		t.Fatalf("stale lease renewed: %v", err)
	}
	if err = s.RenewTemplateBuild(ctx, *job, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateTemplateBuild(ctx, *job); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetTemplateTag(ctx, tenant, item.ID, "default", api.SetTemplateTag{BuildID: replacement}); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteTemplateBuild(ctx, *job, strings.Repeat("c", 64), "isodo-templates"); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetTemplateTag(ctx, tenant, item.ID, "default")
	if err != nil || current.BuildID != replacement {
		t.Fatalf("concurrent tag update overwritten: %+v %v", current, err)
	}
	completed, err := s.GetTemplateBuild(ctx, tenant, item.ID, job.ID)
	if err != nil || completed.Status != "ready" || completed.PublicationError == nil {
		t.Fatalf("Ready untagged build not observable: %+v %v", completed, err)
	}
	if err = s.RenewTemplateBuild(ctx, *job, 30*time.Second); !errors.Is(err, template.ErrLeaseLost) {
		t.Fatalf("terminal build lease still active: %v", err)
	}
	job, err = s.ClaimTemplateBuild(ctx, 30*time.Second)
	if err != nil || job == nil || job.ID != second.ID {
		t.Fatalf("second job not claimed: %+v %v", job, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE template_builds SET lease_expires_at=now()-interval '1 second' WHERE id=$1::uuid`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateTemplateBuild(ctx, *job); !errors.Is(err, template.ErrLeaseLost) {
		t.Fatalf("expired worker validated: %v", err)
	}
	if next, err := s.ClaimTemplateBuild(ctx, 30*time.Second); err != nil || next != nil {
		t.Fatalf("expired build was retried with same ID: %+v %v", next, err)
	}
	expired, err := s.GetTemplateBuild(ctx, tenant, item.ID, job.ID)
	if err != nil || expired.Status != "failed" {
		t.Fatalf("expired build not failed: %+v %v", expired, err)
	}
	if err = s.CompleteTemplateBuild(ctx, *job, strings.Repeat("d", 64), "isodo-templates"); !errors.Is(err, template.ErrLeaseLost) {
		t.Fatalf("expired worker published: %v", err)
	}
	third := enqueue()
	job, err = s.ClaimTemplateBuild(ctx, 30*time.Second)
	if err != nil || job == nil || job.ID != third.ID {
		t.Fatalf("new attempt not claimed: %+v %v", job, err)
	}
	if err = s.ValidateTemplateBuild(ctx, *job); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteTemplateBuild(ctx, *job, strings.Repeat("e", 64), "isodo-templates"); err != nil {
		t.Fatal(err)
	}
	current, err = s.GetTemplateTag(ctx, tenant, item.ID, "default")
	if err != nil || current.BuildID != third.ID {
		t.Fatalf("successful build not promoted: %+v %v", current, err)
	}
}
