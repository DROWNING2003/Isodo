package lima

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrTemplateAlreadyExists = errors.New("Lima template build already exists")

// TemplateInstanceName maps the stable template/build pair into this Node's
// private Lima namespace without exposing either identifier in the VM name.
func (m *Manager) TemplateInstanceName(templateID, buildID string) (string, error) {
	if templateID == "" || buildID == "" || len(templateID) > 256 || len(buildID) > 256 {
		return "", errors.New("template ID and build ID are required and must not exceed 256 bytes")
	}
	digest := sha256.Sum256([]byte(templateID + "\x00" + buildID))
	return m.config.InstancePrefix + "tpl-" + hex.EncodeToString(digest[:16]), nil
}

// TemplateBuilt reports whether this Node already holds the immutable build.
func (m *Manager) TemplateBuilt(ctx context.Context, templateID, buildID string) (bool, error) {
	name, err := m.TemplateInstanceName(templateID, buildID)
	if err != nil {
		return false, err
	}
	instance, err := m.inspectName(ctx, name)
	if errors.Is(err, ErrInstanceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(instance.Status, "stopped"), nil
}

// BuildTemplate creates one immutable Lima template VM directly from the
// Guest-Agent-enabled Base. Commands are argv arrays and execute only in that
// temporary Guest; no host shell is involved.
func (m *Manager) BuildTemplate(ctx context.Context, templateID, buildID string, commands [][]string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	templateName, err := m.TemplateInstanceName(templateID, buildID)
	if err != nil {
		return Instance{}, err
	}
	if err := validateTemplateCommands(commands); err != nil {
		return Instance{}, err
	}
	if _, err := m.inspectName(ctx, templateName); err == nil {
		return Instance{}, ErrTemplateAlreadyExists
	} else if !errors.Is(err, ErrInstanceNotFound) {
		return Instance{}, err
	}
	baseName, err := m.ensureBaseLocked(ctx)
	if err != nil {
		return Instance{}, err
	}
	if _, err := m.run(ctx, "clone", baseName, templateName,
		"--cpus", fmt.Sprint(m.config.CPUs),
		"--memory", fmt.Sprint(m.config.MemoryGiB),
		"--disk", fmt.Sprint(m.config.DiskGiB),
		"--mount-none"); err != nil {
		return Instance{}, fmt.Errorf("clone Isodo Base for template build: %w", err)
	}
	cleanup := func(cause error) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		instance, inspectErr := m.inspectName(cleanupCtx, templateName)
		if errors.Is(inspectErr, ErrInstanceNotFound) {
			return cause
		}
		if inspectErr != nil {
			return errors.Join(cause, fmt.Errorf("inspect failed Lima template build for cleanup: %w", inspectErr))
		}
		if !strings.EqualFold(instance.Status, "stopped") {
			if _, stopErr := m.run(cleanupCtx, "stop", "--yes", templateName); stopErr != nil {
				return errors.Join(cause, fmt.Errorf("stop failed Lima template build: %w", stopErr))
			}
		}
		if _, deleteErr := m.run(cleanupCtx, "delete", "--yes", templateName); deleteErr != nil {
			return errors.Join(cause, fmt.Errorf("delete failed Lima template build: %w", deleteErr))
		}
		return cause
	}
	if _, err := m.run(ctx, "start", "--yes", templateName); err != nil {
		return Instance{}, cleanup(fmt.Errorf("start Lima template builder: %w", err))
	}
	for index, argv := range commands {
		if _, err := m.run(ctx, append([]string{"shell", templateName, "--"}, argv...)...); err != nil {
			return Instance{}, cleanup(fmt.Errorf("run template build command %d: %w", index+1, err))
		}
	}
	if _, err := m.run(ctx, "shell", templateName, "--", "sudo", "systemctl", "is-active", "--quiet", "isodo-guest.service"); err != nil {
		return Instance{}, cleanup(fmt.Errorf("template build did not retain the Isodo Guest Agent: %w", err))
	}
	if _, err := m.run(ctx, "stop", "--yes", templateName); err != nil {
		return Instance{}, cleanup(fmt.Errorf("stop Lima template build: %w", err))
	}
	return m.inspectName(ctx, templateName)
}

// DeleteTemplate removes a stopped template build owned by this Node.
func (m *Manager) DeleteTemplate(ctx context.Context, templateID, buildID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	templateName, err := m.TemplateInstanceName(templateID, buildID)
	if err != nil {
		return err
	}
	instance, err := m.inspectName(ctx, templateName)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(instance.Status, "stopped") {
		return errors.New("Lima template build must be stopped before deletion")
	}
	if _, err := m.run(ctx, "delete", "--yes", templateName); err != nil {
		return fmt.Errorf("delete Lima template build: %w", err)
	}
	return nil
}

func validateTemplateCommands(commands [][]string) error {
	if len(commands) > 32 {
		return errors.New("a Lima template build may include at most 32 commands")
	}
	for _, argv := range commands {
		if len(argv) == 0 || len(argv) > 128 || argv[0] == "" {
			return errors.New("each Lima template build command requires 1 to 128 argv entries")
		}
		for _, arg := range argv {
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return errors.New("Lima template build command arguments must be at most 8192 bytes and contain no NUL")
			}
		}
	}
	return nil
}
