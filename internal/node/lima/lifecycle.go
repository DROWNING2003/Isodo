package lima

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrInstanceExists = errors.New("Lima instance already exists")

type Resources struct {
	CPUs        int
	MemoryBytes uint64
	DiskBytes   uint64
}

func (m *Manager) DefaultResources() Resources {
	return Resources{
		CPUs:        m.config.CPUs,
		MemoryBytes: uint64(m.config.MemoryGiB) << 30,
		DiskBytes:   uint64(m.config.DiskGiB) << 30,
	}
}

// Create creates and starts a new instance, rejecting any pre-existing name.
func (m *Manager) Create(ctx context.Context, sandboxID string) (Instance, error) {
	return m.CreateFromTemplate(ctx, sandboxID, "", "")
}

// CreateFromTemplate creates a Sandbox from a fixed Lima template build. An
// empty pair selects the Guest-Agent-enabled system Base.
func (m *Manager) CreateFromTemplate(ctx context.Context, sandboxID, templateID, buildID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, err := m.NameForSandbox(sandboxID)
	if err != nil {
		return Instance{}, err
	}
	if _, err := m.inspect(ctx, sandboxID); err == nil {
		return Instance{}, ErrInstanceExists
	} else if !errors.Is(err, ErrInstanceNotFound) {
		return Instance{}, err
	}
	source, err := m.templateSourceLocked(ctx, templateID, buildID)
	if err != nil {
		return Instance{}, err
	}
	return m.cloneTemplateLocked(ctx, sandboxID, name, source)
}

// StartExisting starts an existing instance and never creates a replacement.
func (m *Manager) StartExisting(ctx context.Context, sandboxID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	instance, err := m.inspect(ctx, sandboxID)
	if err != nil {
		return Instance{}, err
	}
	if strings.EqualFold(instance.Status, "running") {
		return instance, nil
	}
	if _, err := m.run(ctx, "start", "--yes", instance.Name); err != nil {
		return Instance{}, err
	}
	return m.inspect(ctx, sandboxID)
}

// StartFromTemplate resumes an existing Sandbox or recreates a missing one
// from the same immutable build recorded by its Create operation.
func (m *Manager) StartFromTemplate(ctx context.Context, sandboxID, templateID, buildID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, err := m.NameForSandbox(sandboxID)
	if err != nil {
		return Instance{}, err
	}
	instance, err := m.inspect(ctx, sandboxID)
	if errors.Is(err, ErrInstanceNotFound) {
		source, err := m.templateSourceLocked(ctx, templateID, buildID)
		if err != nil {
			return Instance{}, err
		}
		return m.cloneTemplateLocked(ctx, sandboxID, name, source)
	}
	if err != nil {
		return Instance{}, err
	}
	if strings.EqualFold(instance.Status, "running") {
		return instance, nil
	}
	if _, err := m.run(ctx, "start", "--yes", name); err != nil {
		return Instance{}, err
	}
	return m.inspect(ctx, sandboxID)
}

func (m *Manager) templateSourceLocked(ctx context.Context, templateID, buildID string) (string, error) {
	if templateID == "" && buildID == "" {
		return m.ensureBaseLocked(ctx)
	}
	if templateID == "" || buildID == "" {
		return "", errors.New("template ID and build ID must be provided together")
	}
	templateName, err := m.TemplateInstanceName(templateID, buildID)
	if err != nil {
		return "", err
	}
	instance, err := m.inspectName(ctx, templateName)
	if err != nil {
		return "", fmt.Errorf("resolve Lima template build: %w", err)
	}
	if !strings.EqualFold(instance.Status, "stopped") {
		return "", errors.New("Lima template build must be stopped before cloning")
	}
	return templateName, nil
}
