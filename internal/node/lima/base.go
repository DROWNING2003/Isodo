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
	"os"
	"strconv"
	"strings"
)

const baseProvisionerVersion = "v2"

const t3ServerGuestPort = 8080

// BaseInstanceName identifies the immutable OS + Guest Agent layer.
func (m *Manager) BaseInstanceName() (string, error) {
	if m.config.GuestAgentBinary == "" {
		return "", errors.New("Isodo Guest Agent binary path is required for Lima Base creation")
	}
	binary, err := os.ReadFile(m.config.GuestAgentBinary)
	if err != nil {
		return "", fmt.Errorf("read Isodo Guest Agent binary: %w", err)
	}
	digest := sha256.Sum256(append([]byte(m.config.Template+"\x00"+baseProvisionerVersion+"\x00"), binary...))
	return m.config.InstancePrefix + "base-" + hex.EncodeToString(digest[:6]), nil
}

// EnsureBase prepares the Guest Agent once and keeps the versioned Base stopped.
func (m *Manager) EnsureBase(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureBaseLocked(ctx)
}

func (m *Manager) ensureBaseLocked(ctx context.Context) (string, error) {
	baseName, err := m.BaseInstanceName()
	if err != nil {
		return "", err
	}
	instance, err := m.inspectName(ctx, baseName)
	if errors.Is(err, ErrInstanceNotFound) {
		_, err = m.run(ctx, "create", "--yes", "--name", baseName,
			"--vm-type", m.config.VMType,
			"--cpus", strconv.Itoa(m.config.CPUs),
			"--memory", strconv.Itoa(m.config.MemoryGiB),
			"--disk", strconv.Itoa(m.config.DiskGiB),
			"--mount-none", "--containerd", "none", m.config.Template)
		if err != nil {
			return "", fmt.Errorf("create Isodo Lima Base: %w", err)
		}
		if _, err := m.run(ctx, "start", "--yes", baseName); err != nil {
			return "", fmt.Errorf("start Isodo Lima Base: %w", err)
		}
		instance, err = m.inspectName(ctx, baseName)
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if !strings.EqualFold(instance.Status, "running") {
		if _, err := m.run(ctx, "start", "--yes", baseName); err != nil {
			return "", fmt.Errorf("start Isodo Lima Base for provisioning: %w", err)
		}
	}
	if _, err := m.run(ctx, "shell", baseName, "--", "sudo", "test", "-f", "/var/lib/isodo/base-ready"); err != nil {
		if err := m.provisionGuestLocked(ctx, baseName); err != nil {
			return "", fmt.Errorf("provision Isodo Lima Base Guest Agent: %w", err)
		}
	}
	if m.config.EnableT3Runtime {
		if err := m.provisionT3RuntimeLocked(ctx, baseName); err != nil {
			return "", fmt.Errorf("provision T3 and Agent runtimes in Lima Base: %w", err)
		}
	}
	if _, err := m.run(ctx, "stop", "--yes", baseName); err != nil {
		return "", fmt.Errorf("stop Isodo Lima Base after provisioning: %w", err)
	}
	return baseName, nil
}

func (m *Manager) cloneBaseLocked(ctx context.Context, sandboxID, name string) (Instance, error) {
	baseName, err := m.ensureBaseLocked(ctx)
	if err != nil {
		return Instance{}, err
	}
	return m.cloneTemplateLocked(ctx, sandboxID, name, baseName)
}

func (m *Manager) cloneTemplateLocked(ctx context.Context, sandboxID, name, templateName string) (Instance, error) {
	port, err := m.GuestAgentHostPort(sandboxID)
	if err != nil {
		return Instance{}, err
	}
	if _, err := m.run(ctx, "clone", templateName, name,
		"--cpus", strconv.Itoa(m.config.CPUs),
		"--memory", strconv.Itoa(m.config.MemoryGiB),
		"--disk", strconv.Itoa(m.config.DiskGiB),
		"--mount-none"); err != nil {
		return Instance{}, fmt.Errorf("clone Sandbox VM from Isodo Base: %w", err)
	}
	appPort, err := m.SandboxAppHostPort(sandboxID)
	if err != nil {
		return Instance{}, err
	}
	portForward := fmt.Sprintf(".portForwards = [{\"guestPort\": %d, \"hostPort\": %d, \"guestIP\": \"127.0.0.1\", \"hostIP\": \"127.0.0.1\", \"static\": true}, {\"guestPort\": %d, \"hostPort\": %d, \"guestIP\": \"127.0.0.1\", \"hostIP\": \"127.0.0.1\", \"static\": true}]", guestAgentPort, port, t3ServerGuestPort, appPort)
	if _, err := m.run(ctx, "edit", name, "--set", portForward); err != nil {
		return Instance{}, fmt.Errorf("configure Lima Guest Agent and T3 app port forwards: %w", err)
	}
	if _, err := m.run(ctx, "start", "--yes", name); err != nil {
		return Instance{}, fmt.Errorf("start Lima Sandbox clone: %w", err)
	}
	return m.inspectName(ctx, name)
}

func (m *Manager) inspectName(ctx context.Context, name string) (Instance, error) {
	output, err := m.run(ctx, "list", "--format", "json")
	if err != nil {
		return Instance{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var instance Instance
		if err := decoder.Decode(&instance); errors.Is(err, io.EOF) {
			return Instance{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, name)
		} else if err != nil {
			return Instance{}, fmt.Errorf("decode Lima instance list: %w", err)
		}
		if instance.Name == name {
			return instance, nil
		}
	}
}

// GuestAgentHostPort returns the deterministic loopback-only host port for a Sandbox.
func (m *Manager) GuestAgentHostPort(sandboxID string) (uint16, error) {
	if _, err := m.NameForSandbox(sandboxID); err != nil {
		return 0, err
	}
	digest := sha256.Sum256([]byte("guest-agent-port:" + sandboxID))
	return uint16(40000 + (uint16(digest[0])<<8|uint16(digest[1]))%20000), nil
}

func (m *Manager) GuestEndpoint(sandboxID string) (string, error) {
	port, err := m.GuestAgentHostPort(sandboxID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

// SandboxAppHostPort returns the loopback-only host port forwarded to the T3 server.
func (m *Manager) SandboxAppHostPort(sandboxID string) (uint16, error) {
	if _, err := m.NameForSandbox(sandboxID); err != nil {
		return 0, err
	}
	digest := sha256.Sum256([]byte("sandbox-app-port:" + sandboxID))
	return uint16(20000 + (uint16(digest[0])<<8|uint16(digest[1]))%20000), nil
}

// SandboxAppURL gives each Sandbox a separate host-only origin so browser
// cookies for its T3 session cannot collide with another Sandbox's cookies.
func (m *Manager) SandboxAppURL(sandboxID string) (string, error) {
	port, err := m.SandboxAppHostPort(sandboxID)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("sandbox-app-host:" + sandboxID))
	host := hex.EncodeToString(digest[:8])
	return fmt.Sprintf("http://sbx-%s.localhost:%d", host, port), nil
}

func (m *Manager) provisionT3RuntimeLocked(ctx context.Context, instanceName string) error {
	if _, err := m.run(ctx, "shell", instanceName, "--", "sudo", "test", "-f", "/var/lib/isodo/t3-runtime-ready"); err == nil {
		return nil
	}
	const script = `set -eu
apt-get update
apt-get install -y ca-certificates curl xz-utils
architecture=$(uname -m)
case "$architecture" in
  x86_64) architecture=x64 ;;
  aarch64|arm64) architecture=arm64 ;;
  *) echo "unsupported Lima Guest architecture: $architecture" >&2; exit 1 ;;
esac
version=v24.13.1
archive="node-${version}-linux-${architecture}.tar.xz"
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
curl -fsSL "https://nodejs.org/dist/${version}/${archive}" -o "$temporary/$archive"
curl -fsSL "https://nodejs.org/dist/${version}/SHASUMS256.txt" -o "$temporary/SHASUMS256.txt"
cd "$temporary"
grep " ${archive}$" SHASUMS256.txt | sha256sum -c -
tar -xJf "$archive" -C /usr/local --strip-components=1
npm install --global --prefix /opt/isodo-tools @openai/codex@0.130.0 @anthropic-ai/claude-code@2.1.310
ln -sfn /opt/isodo-tools/bin/codex /usr/local/bin/codex
ln -sfn /opt/isodo-tools/bin/claude /usr/local/bin/claude
node --version
codex --version
claude --version
install -D -m 0644 /dev/null /var/lib/isodo/t3-runtime-ready
`
	if _, err := m.run(ctx, "shell", instanceName, "--", "sudo", "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("install Node.js and pinned Agent CLIs in Lima Base: %w", err)
	}
	return nil
}

func (m *Manager) provisionGuestLocked(ctx context.Context, instanceName string) error {
	info, err := os.Stat(m.config.GuestAgentBinary)
	if err != nil {
		return fmt.Errorf("inspect Isodo Guest Agent binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("Isodo Guest Agent binary must be an executable regular file")
	}
	if _, err := m.run(ctx, "copy", m.config.GuestAgentBinary, instanceName+":/tmp/isodo-guest"); err != nil {
		return fmt.Errorf("copy Isodo Guest Agent into Lima Base: %w", err)
	}
	unit, err := os.CreateTemp("", "isodo-guest-*.service")
	if err != nil {
		return fmt.Errorf("create temporary Guest Agent unit: %w", err)
	}
	unitPath := unit.Name()
	defer os.Remove(unitPath)
	if _, err := fmt.Fprintf(unit, `[Unit]
Description=Isodo Guest Agent
After=network.target

[Service]
Type=simple
User=nobody
Group=nogroup
Environment=ISODO_TEMPLATE_MODE=true
Environment=ISODO_GUEST_TRANSPORT=tcp
Environment=ISODO_GUEST_LISTEN_ADDRESS=127.0.0.1:%d
Environment=HOME=/workspace/.home
Environment=PATH=/usr/local/bin:/usr/bin:/bin
ExecStart=/usr/local/bin/isodo-guest
Restart=on-failure
RestartSec=1
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/workspace

[Install]
WantedBy=multi-user.target
`, guestAgentPort); err != nil {
		_ = unit.Close()
		return fmt.Errorf("write Guest Agent unit: %w", err)
	}
	if err := unit.Close(); err != nil {
		return err
	}
	if _, err := m.run(ctx, "copy", unitPath, instanceName+":/tmp/isodo-guest.service"); err != nil {
		return fmt.Errorf("copy Isodo Guest Agent unit into Lima Base: %w", err)
	}
	commands := [][]string{
		{"sudo", "install", "-m", "0755", "/tmp/isodo-guest", "/usr/local/bin/isodo-guest"},
		{"sudo", "install", "-m", "0644", "/tmp/isodo-guest.service", "/etc/systemd/system/isodo-guest.service"},
		{"sudo", "install", "-d", "-o", "nobody", "-g", "nogroup", "-m", "0750", "/workspace"},
		{"sudo", "install", "-d", "-o", "nobody", "-g", "nogroup", "-m", "0700", "/workspace/.home"},
		{"sudo", "install", "-d", "-m", "0755", "/var/lib/isodo"},
		{"sudo", "systemctl", "daemon-reload"},
		{"sudo", "systemctl", "enable", "--now", "isodo-guest.service"},
		{"sudo", "install", "-m", "0444", "/dev/null", "/var/lib/isodo/base-ready"},
	}
	for _, command := range commands {
		if _, err := m.run(ctx, append([]string{"shell", instanceName, "--"}, command...)...); err != nil {
			return fmt.Errorf("provision Isodo Guest Agent Base (%s): %w", command[1], err)
		}
	}
	return nil
}

func (m *Manager) BootID(ctx context.Context, sandboxID string) (string, error) {
	instance, err := m.Inspect(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	output, err := m.run(ctx, "shell", instance.Name, "--", "cat", "/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read Lima Guest boot ID: %w", err)
	}
	bootID := strings.TrimSpace(string(output))
	if bootID == "" {
		return "", errors.New("Lima Guest returned an empty boot ID")
	}
	return bootID, nil
}
