package postgres

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/template"
	"github.com/google/uuid"
)

func TestSystemPublicationCASAndReadOnlyCatalog(t *testing.T) {
	url := os.Getenv("ISODO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	s, err := New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	name := "system-" + uuid.NewString()
	digest := strings.Repeat("a", 64)
	// Database fixtures prove publication atomicity, not Guest/S3 validation.
	publications := make(chan SystemPublication, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			p, e := s.RegisterSystemTemplate(t.Context(), name, "test", digest, "test-bucket", "")
			if e != nil {
				failures <- e
			} else {
				publications <- p
			}
		})
	}
	wg.Wait()
	close(publications)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var first SystemPublication
	for p := range publications {
		if first.BuildID == "" {
			first = p
		}
		if p != first {
			t.Errorf("retry created another publication: %+v", p)
		}
	}
	if first.BuildID == "" {
		t.Fatal("missing publication")
	}
	var count int
	if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM template_builds WHERE template_id=$1::uuid`, first.TemplateID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("build count=%d error=%v", count, err)
	}
	_, err = s.RegisterSystemTemplate(t.Context(), name, "test", strings.Repeat("b", 64), "test-bucket", "")
	if !errors.Is(err, template.ErrConflict) {
		t.Fatalf("unconditional replacement: %v", err)
	}
	next, err := s.RegisterSystemTemplate(t.Context(), name, "test", strings.Repeat("b", 64), "test-bucket", first.BuildID)
	if err != nil {
		t.Fatal(err)
	}
	if next.TemplateID != first.TemplateID || next.BuildID == first.BuildID {
		t.Fatal("replacement did not retain template and create new immutable build")
	}
	_, err = s.RegisterSystemTemplate(t.Context(), name, "test", digest, "test-bucket", first.BuildID)
	if !errors.Is(err, template.ErrConflict) {
		t.Fatalf("stale replacement: %v", err)
	}
	tenant, err := s.ResolveTenant(t.Context(), "https://"+name+".test", "viewer", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.GetTemplate(t.Context(), tenant, first.TemplateID)
	if err != nil || visible.Owned || visible.Scope != api.TemplateScopeSystem {
		t.Fatalf("public system template: %+v %v", visible, err)
	}
	_, err = s.UpdateTemplate(t.Context(), tenant, first.TemplateID, api.PatchTemplate{Name: new("modified")})
	if !errors.Is(err, template.ErrNotFound) {
		t.Fatalf("personal user modified system template: %v", err)
	}
	tag, err := s.GetTemplateTag(t.Context(), tenant, first.TemplateID, "default")
	if err != nil || tag.BuildID != next.BuildID {
		t.Fatalf("default tag=%+v error=%v", tag, err)
	}
}
