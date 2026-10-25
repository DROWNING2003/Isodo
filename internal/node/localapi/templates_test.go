package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "templates.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCatalogAlwaysOffersBase(t *testing.T) {
	catalog, err := NewTemplateCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Find(BaseTemplateID); !ok {
		t.Fatal("base template is missing from the catalog")
	}
}

func TestCatalogRejectsReservedAndDuplicateIDs(t *testing.T) {
	for name, body := range map[string]string{
		"reserved":  `{"templates":[{"id":"base","commands":[["true"]]}]}`,
		"duplicate": `{"templates":[{"id":"node","commands":[["true"]]},{"id":"node","commands":[["true"]]}]}`,
		"noCommand": `{"templates":[{"id":"node","commands":[]}]}`,
		"badID":     `{"templates":[{"id":"Node 20","commands":[["true"]]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTemplateCatalog(writeCatalog(t, body)); err == nil {
				t.Fatal("catalog accepted an invalid template")
			}
		})
	}
}

func TestBuildIDChangesWhenCommandsChange(t *testing.T) {
	first := TemplateSpec{ID: "node", Commands: [][]string{{"node", "--version"}}}
	second := TemplateSpec{ID: "node", Commands: [][]string{{"node", "--version"}, {"true"}}}
	if first.BuildID() == second.BuildID() {
		t.Fatal("editing the recipe must produce a new immutable build")
	}
	if first.BuildID() != (TemplateSpec{ID: "node", Commands: [][]string{{"node", "--version"}}}).BuildID() {
		t.Fatal("build ID must be stable for an unchanged recipe")
	}
}

type fakeBuilder struct {
	built    map[string]bool
	requests chan string
}

func (f *fakeBuilder) TemplateBuilt(_ context.Context, templateID, buildID string) (bool, error) {
	return f.built[templateID+"/"+buildID], nil
}

func (f *fakeBuilder) BuildTemplate(_ context.Context, templateID, buildID string, commands [][]string) (string, error) {
	if f.requests != nil {
		f.requests <- templateID
	}
	f.built[templateID+"/"+buildID] = true
	return "image-" + templateID, nil
}

func newTemplateServer(t *testing.T, builder TemplateBuilder) (*Server, *TemplateCatalog) {
	t.Helper()
	catalog, err := NewTemplateCatalog(writeCatalog(t, `{"templates":[{"id":"node","name":"Node 22","commands":[["node","--version"]]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(fakeNode{}, "secret", catalog, builder, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server, catalog
}

func authorizedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	return request
}

func TestListTemplatesHidesCommandsAndReportsReadiness(t *testing.T) {
	builder := &fakeBuilder{built: map[string]bool{}}
	server, catalog := newTemplateServer(t, builder)

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/templates", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.Contains(response.Body.String(), "node --version") {
		t.Fatal("build commands must not leak to the client")
	}
	var payload struct {
		Items []TemplateView `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("items = %d, want base, python, and node", len(payload.Items))
	}
	for _, item := range payload.Items {
		wantReady := item.ID == BaseTemplateID
		if item.Ready != wantReady {
			t.Fatalf("readiness = %+v, want base ready and every buildable template unbuilt", payload.Items)
		}
	}

	spec, _ := catalog.Find("node")
	builder.built["node/"+spec.BuildID()] = true
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/templates", ""))
	if !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("built template still reported unbuilt: %s", response.Body.String())
	}
}

func TestBuildTemplateReturnsImmediatelyAndBecomesReady(t *testing.T) {
	builder := &fakeBuilder{built: map[string]bool{}, requests: make(chan string, 1)}
	server, _ := newTemplateServer(t, builder)

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/templates/node/build", ""))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	select {
	case id := <-builder.requests:
		if id != "node" {
			t.Fatalf("build ran for %q, want node", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("template build never started")
	}
}

func TestCreateSandboxRejectsUnbuiltTemplate(t *testing.T) {
	server, _ := newTemplateServer(t, &fakeBuilder{built: map[string]bool{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/sandboxes", `{"template":"node"}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
}

func TestCreateSandboxRejectsUnknownTemplate(t *testing.T) {
	server, _ := newTemplateServer(t, &fakeBuilder{built: map[string]bool{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/sandboxes", `{"template":"missing"}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
