package lima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureBaseReusesExistingStoppedBase(t *testing.T) {
	temp := t.TempDir()
	guestBinary := filepath.Join(temp, "isodo-guest")
	if err := os.WriteFile(guestBinary, []byte("guest"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(temp, "limactl.log")
	scriptPath := filepath.Join(temp, "limactl")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$ISODO_LIMA_TEST_LOG"
if [ "$1" = list ]; then
  printf '{"name":"%s","status":"stopped"}\n' "$ISODO_LIMA_TEST_BASE"
fi
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		Binary:           scriptPath,
		InstancePrefix:   "isodo-test-",
		Template:         "template:ubuntu-24.04",
		GuestAgentBinary: guestBinary,
		VMType:           "qemu",
		CPUs:             1,
		MemoryGiB:        1,
		DiskGiB:          5,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseName, err := manager.BaseInstanceName()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISODO_LIMA_TEST_LOG", logPath)
	t.Setenv("ISODO_LIMA_TEST_BASE", baseName)

	got, err := manager.EnsureBase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != baseName {
		t.Fatalf("EnsureBase() = %q, want %q", got, baseName)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range strings.Fields(string(log)) {
		if command == "start" || command == "shell" || command == "stop" {
			t.Fatalf("existing stopped Base was started or reprovisioned: %q", string(log))
		}
	}
}
