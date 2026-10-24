package postgres

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	api "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/google/uuid"
)

func TestSandboxPinsAuthorizedReadyTemplate(t *testing.T) {
	url := os.Getenv("ISODO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated migrated PostgreSQL")
	}
	s, err := New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.ResolveTenant(t.Context(), "https://pin-"+uuid.NewString()+".test", "owner", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.ResolveTenant(t.Context(), "https://pin-"+uuid.NewString()+".test", "viewer", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.CreateTemplate(t.Context(), owner, api.CreateTemplate{Name: "private-base"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(tenant, ref string) (sandbox.Sandbox, error) {
		return s.CreateSandbox(t.Context(), tenant, "pinned", json.RawMessage(`{}`), ref)
	}
	if _, err = create(owner, item.ID); !errors.Is(err, sandbox.ErrTemplateUnavailable) {
		t.Fatalf("accepted unbuilt template: %v", err)
	}
	fixture := func(digest string) string {
		t.Helper()
		var id string
		err := s.pool.QueryRow(t.Context(), `INSERT INTO template_builds(template_id,status,manifest_digest,artifact_bucket,artifact_prefix) VALUES($1::uuid,'ready',$2,'test-bucket',$3) RETURNING id::text`, item.ID, digest, "templates/sha256/"+digest).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := fixture(strings.Repeat("a", 64))
	next := fixture(strings.Repeat("b", 64))
	if _, err = s.SetTemplateTag(t.Context(), owner, item.ID, "default", api.SetTemplateTag{BuildID: first}); err != nil {
		t.Fatal(err)
	}
	if _, err = create(viewer, item.ID); !errors.Is(err, sandbox.ErrTemplateUnavailable) {
		t.Fatalf("foreign private template: %v", err)
	}
	pinned, err := create(owner, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.TemplateBuildID == nil || *pinned.TemplateBuildID != first || pinned.TemplateDigest == nil || *pinned.TemplateDigest != strings.Repeat("a", 64) {
		t.Fatalf("incorrect pin: %+v", pinned)
	}
	if _, err = s.SetTemplateTag(t.Context(), owner, item.ID, "default", api.SetTemplateTag{BuildID: next}); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetSandbox(t.Context(), owner, pinned.ID)
	if err != nil || loaded.TemplateBuildID == nil || *loaded.TemplateBuildID != first {
		t.Fatalf("tag move changed existing sandbox: %+v %v", loaded, err)
	}
	later, err := create(owner, item.ID)
	if err != nil || later.TemplateBuildID == nil || *later.TemplateBuildID != next {
		t.Fatalf("new sandbox missed moved tag: %+v %v", later, err)
	}
	if _, err = s.UpdateTemplate(t.Context(), owner, item.ID, api.PatchTemplate{Visibility: new(api.PatchTemplateVisibility("public"))}); err != nil {
		t.Fatal(err)
	}
	if _, err = create(viewer, item.ID); err != nil {
		t.Fatalf("public template unavailable: %v", err)
	}
	if _, err = s.UpdateTemplate(t.Context(), owner, item.ID, api.PatchTemplate{Visibility: new(api.PatchTemplateVisibility("private"))}); err != nil {
		t.Fatal(err)
	}
	if _, err = create(viewer, item.ID); !errors.Is(err, sandbox.ErrTemplateUnavailable) {
		t.Fatalf("new sandbox used hidden template: %v", err)
	}
	if err = s.DeleteTemplate(t.Context(), owner, item.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.GetSandbox(t.Context(), owner, pinned.ID)
	if err != nil || loaded.TemplateBuildID == nil || *loaded.TemplateBuildID != first {
		t.Fatalf("soft-delete damaged pin: %+v %v", loaded, err)
	}
	if _, err = create(owner, item.ID); !errors.Is(err, sandbox.ErrTemplateUnavailable) {
		t.Fatalf("new sandbox used deleted template: %v", err)
	}
}
