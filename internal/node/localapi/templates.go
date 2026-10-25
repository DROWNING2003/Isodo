package localapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// BaseTemplateID is the built-in template every Node can create sandboxes from.
// It needs no build step because the runtime provisions it on first use.
const BaseTemplateID = "base"

var templateIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// TemplateSpec is a local template: a display name plus the commands replayed
// once inside a guest VM to produce an immutable image that sandboxes clone.
// Users only ever see the name; the commands never leave the Node.
type TemplateSpec struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Commands    [][]string `json:"commands"`
}

// BuildID is derived from the spec so an edited recipe materializes as a new
// immutable build instead of mutating the previous one.
func (s TemplateSpec) BuildID() string {
	payload, _ := json.Marshal(struct {
		ID       string     `json:"id"`
		Commands [][]string `json:"commands"`
	}{ID: s.ID, Commands: s.Commands})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:16])
}

// TemplateCatalog lists the local templates a Node can create sandboxes from.
type TemplateCatalog struct {
	specs []TemplateSpec
}

// NewTemplateCatalog builds the catalog: the built-in base plus every spec in
// the optional JSON file. A missing file is not an error; it only means the
// Node offers its built-in base.
func NewTemplateCatalog(path string) (*TemplateCatalog, error) {
	specs := []TemplateSpec{
		{
			ID:          BaseTemplateID,
			Name:        "base",
			Description: "Isodo Linux base with the Guest Agent, Node.js, and the T3 runtime.",
		},
		{
			ID:          "python",
			Name:        "Python 3",
			Description: "Isodo Linux base plus Python 3, pip, and venv.",
			Commands: [][]string{
				{"sudo", "apt-get", "update"},
				{"sudo", "apt-get", "install", "-y", "--no-install-recommends", "python3", "python3-pip", "python3-venv"},
			},
		},
	}
	if strings.TrimSpace(path) != "" {
		content, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read local template catalog: %w", err)
		}
		if err == nil {
			var document struct {
				Templates []TemplateSpec `json:"templates"`
			}
			if err := json.Unmarshal(content, &document); err != nil {
				return nil, fmt.Errorf("parse local template catalog: %w", err)
			}
			for _, spec := range document.Templates {
				if err := validateTemplateSpec(spec); err != nil {
					return nil, err
				}
				if slices.ContainsFunc(specs, func(existing TemplateSpec) bool { return existing.ID == spec.ID }) {
					return nil, fmt.Errorf("local template %q is declared twice", spec.ID)
				}
				if spec.Name == "" {
					spec.Name = spec.ID
				}
				specs = append(specs, spec)
			}
		}
	}
	return &TemplateCatalog{specs: specs}, nil
}

func validateTemplateSpec(spec TemplateSpec) error {
	if !templateIDPattern.MatchString(spec.ID) {
		return fmt.Errorf("local template ID %q must match %s", spec.ID, templateIDPattern)
	}
	if spec.ID == BaseTemplateID {
		return fmt.Errorf("local template %q is reserved for the built-in base", BaseTemplateID)
	}
	if len(spec.Commands) == 0 || len(spec.Commands) > 32 {
		return fmt.Errorf("local template %q needs between 1 and 32 build commands", spec.ID)
	}
	for _, argv := range spec.Commands {
		if len(argv) == 0 || argv[0] == "" || len(argv) > 128 {
			return fmt.Errorf("local template %q has a command without an executable", spec.ID)
		}
	}
	return nil
}

// Find returns the spec with the given ID.
func (c *TemplateCatalog) Find(id string) (TemplateSpec, bool) {
	index := slices.IndexFunc(c.specs, func(spec TemplateSpec) bool { return spec.ID == id })
	if index < 0 {
		return TemplateSpec{}, false
	}
	return c.specs[index], true
}

// Specs returns every template in catalog order.
func (c *TemplateCatalog) Specs() []TemplateSpec {
	return slices.Clone(c.specs)
}

// TemplateBuilder materializes and inspects immutable template images. The
// concrete backend (Lima today) stays behind this interface so the HTTP surface
// and the desktop client never depend on it.
type TemplateBuilder interface {
	TemplateBuilt(ctx context.Context, templateID, buildID string) (bool, error)
	BuildTemplate(ctx context.Context, templateID, buildID string, commands [][]string) (string, error)
}

// TemplateView is what the desktop client sees: no commands, no backend paths.
type TemplateView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Ready       bool   `json:"ready"`
	Building    bool   `json:"building"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	views := make([]TemplateView, 0, len(s.catalog.Specs()))
	for _, spec := range s.catalog.Specs() {
		view := TemplateView{ID: spec.ID, Name: spec.Name, Description: spec.Description, Ready: true}
		if spec.ID != BaseTemplateID {
			built, err := s.templateReady(r.Context(), spec.ID, spec.BuildID())
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "TEMPLATE_STATE_UNAVAILABLE", err.Error(), true)
				return
			}
			view.Ready = built
			view.Building = s.builds.busy(spec.ID)
			if !built {
				view.Error = s.builds.failure(spec.ID)
			}
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *Server) templateReady(ctx context.Context, templateID, buildID string) (bool, error) {
	if s.builder == nil {
		return false, nil
	}
	return s.builder.TemplateBuilt(ctx, templateID, buildID)
}

// buildTemplate materializes a template image in the background so the HTTP
// call returns immediately and the client can poll GET /templates.
func (s *Server) buildTemplate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/templates/"), "/build")
	spec, ok := s.catalog.Find(id)
	if !ok || id == BaseTemplateID {
		writeError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "unknown template", false)
		return
	}
	if s.builder == nil {
		writeError(w, http.StatusFailedDependency, "TEMPLATE_BUILD_UNAVAILABLE", "this runtime cannot build templates", false)
		return
	}
	buildID := spec.BuildID()
	built, err := s.templateReady(r.Context(), spec.ID, buildID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "TEMPLATE_STATE_UNAVAILABLE", err.Error(), true)
		return
	}
	if built {
		writeJSON(w, http.StatusOK, TemplateView{ID: spec.ID, Name: spec.Name, Description: spec.Description, Ready: true})
		return
	}
	if !s.builds.start(spec.ID) {
		writeError(w, http.StatusConflict, "TEMPLATE_BUILDING", "this template is already being built", true)
		return
	}
	go func() {
		// Detached from the request: the build outlives the HTTP call.
		_, buildErr := s.builder.BuildTemplate(context.WithoutCancel(r.Context()), spec.ID, buildID, spec.Commands)
		s.builds.finish(spec.ID, buildErr)
	}()
	writeJSON(w, http.StatusAccepted, TemplateView{ID: spec.ID, Name: spec.Name, Description: spec.Description, Building: true})
}

// buildRegistry prevents duplicate concurrent builds of the same template.
type buildRegistry struct {
	mu        sync.Mutex
	running   map[string]struct{}
	lastError map[string]string
}

func (r *buildRegistry) start(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[string]struct{}{}
	}
	if _, busy := r.running[id]; busy {
		return false
	}
	r.running[id] = struct{}{}
	return true
}

func (r *buildRegistry) finish(id string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, id)
	if r.lastError == nil {
		r.lastError = map[string]string{}
	}
	if err != nil {
		r.lastError[id] = err.Error()
		return
	}
	delete(r.lastError, id)
}

// failure reports why the last build of this template failed, if it did.
func (r *buildRegistry) failure(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastError[id]
}

func (r *buildRegistry) busy(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, busy := r.running[id]
	return busy
}
