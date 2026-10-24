package lima

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

var ErrInstanceNotFound = errors.New("Lima instance not found")

const guestAgentPort = 1024

// Config controls the host-local Lima instances owned by this Node.
type Config struct {
	Binary           string
	InstancePrefix   string
	Template         string
	GuestAgentBinary string
	EnableT3Runtime  bool
	VMType           string
	CPUs             int
	MemoryGiB        int
	DiskGiB          int
}

// Instance is the subset of Lima instance state used by the Node.
type Instance struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Manager controls only instances in its own deterministic name namespace.
type Manager struct {
	config Config
	mu     sync.Mutex
}

func NewManager(config Config) (*Manager, error) {
	if config.Binary == "" {
		config.Binary = "limactl"
	}
	if config.InstancePrefix == "" {
		config.InstancePrefix = "isodo-sbx-"
	}
	if config.Template == "" {
		config.Template = "template:ubuntu-24.04"
	}
	if config.VMType == "" {
		config.VMType = "vz"
	}
	if config.CPUs == 0 {
		config.CPUs = 2
	}
	if config.MemoryGiB == 0 {
		config.MemoryGiB = 4
	}
	if config.DiskGiB == 0 {
		config.DiskGiB = 20
	}

	if !validPrefix(config.InstancePrefix) {
		return nil, errors.New("Lima instance prefix must be lowercase alphanumeric and end with a hyphen")
	}
	if config.VMType != "vz" && config.VMType != "qemu" {
		return nil, errors.New("Lima VM type must be vz or qemu")
	}
	if config.VMType == "vz" && runtime.GOOS != "darwin" {
		return nil, errors.New("Lima vz backend requires macOS")
	}
	if config.CPUs < 1 || config.CPUs > 16 {
		return nil, errors.New("Lima CPU allocation must be between 1 and 16")
	}
	if config.MemoryGiB < 1 || config.MemoryGiB > 64 {
		return nil, errors.New("Lima memory allocation must be between 1 and 64 GiB")
	}
	if config.DiskGiB < 5 || config.DiskGiB > 256 {
		return nil, errors.New("Lima disk allocation must be between 5 and 256 GiB")
	}
	if strings.TrimSpace(config.Template) == "" || strings.HasPrefix(config.Template, "-") || strings.ContainsAny(config.Template, "\r\n") {
		return nil, errors.New("Lima template must be a non-empty template, URL, or file path")
	}

	return &Manager{config: config}, nil
}

// NameForSandbox returns a stable, shell-safe Lima name without exposing the Sandbox ID.
func (m *Manager) NameForSandbox(sandboxID string) (string, error) {
	if sandboxID == "" || len(sandboxID) > 256 {
		return "", errors.New("sandbox ID is required and must not exceed 256 bytes")
	}
	digest := sha256.Sum256([]byte(sandboxID))
	return m.config.InstancePrefix + hex.EncodeToString(digest[:16]), nil
}

// Inspect returns the Lima state for a Sandbox-owned instance.
func (m *Manager) Inspect(ctx context.Context, sandboxID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inspect(ctx, sandboxID)
}

func (m *Manager) inspect(ctx context.Context, sandboxID string) (Instance, error) {
	name, err := m.NameForSandbox(sandboxID)
	if err != nil {
		return Instance{}, err
	}
	output, err := m.run(ctx, "list", "--format", "json")
	if err != nil {
		return Instance{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var instance Instance
		if err := decoder.Decode(&instance); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return Instance{}, fmt.Errorf("decode Lima instance list: %w", err)
		}
		if instance.Name == name {
			return instance, nil
		}
	}
	return Instance{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, name)
}

// Start resumes an existing Sandbox or creates one as a clone of the immutable Base.
func (m *Manager) Start(ctx context.Context, sandboxID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, err := m.NameForSandbox(sandboxID)
	if err != nil {
		return Instance{}, err
	}
	instance, err := m.inspect(ctx, sandboxID)
	if errors.Is(err, ErrInstanceNotFound) {
		return m.cloneBaseLocked(ctx, sandboxID, name)
	} else if err != nil {
		return Instance{}, err
	} else if strings.EqualFold(instance.Status, "running") {
		return instance, nil
	}

	if _, err := m.run(ctx, "start", "--yes", name); err != nil {
		return Instance{}, err
	}
	return m.inspect(ctx, sandboxID)
}

// Stop preserves the Lima disk and is idempotent for absent or stopped instances.
func (m *Manager) Stop(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, err := m.inspect(ctx, sandboxID)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.EqualFold(instance.Status, "stopped") {
		return nil
	}
	_, err = m.run(ctx, "stop", "--yes", instance.Name)
	return err
}

// Delete removes only the instance name derived from the supplied Sandbox ID.
func (m *Manager) Delete(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, err := m.inspect(ctx, sandboxID)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(instance.Status, "stopped") {
		if _, err := m.run(ctx, "stop", "--yes", instance.Name); err != nil {
			return err
		}
	}
	_, err = m.run(ctx, "delete", "--yes", instance.Name)
	return err
}

func (m *Manager) run(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, m.config.Binary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return nil, fmt.Errorf("run limactl %s: %w", args[0], err)
		}
		return nil, fmt.Errorf("run limactl %s: %w: %s", args[0], err, detail)
	}
	return stdout.Bytes(), nil
}

func validPrefix(prefix string) bool {
	if len(prefix) < 2 || len(prefix) > 23 || prefix[len(prefix)-1] != '-' {
		return false
	}
	for _, char := range prefix[:len(prefix)-1] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
