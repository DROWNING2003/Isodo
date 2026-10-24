package templatebuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
)

func TestRuntimeRejectsExistingDirectoryWithoutRemovingIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("runtime is Linux-only")
	}
	directory, digest := runtimeFixture(t)
	destination := t.TempDir()
	marker := filepath.Join(destination, "keep")
	if err := os.WriteFile(marker, []byte("existing runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := StartRuntime(t.Context(), t.Context(), RuntimeConfig{VMMBinary: "/bin/false", ArtifactDirectory: directory, ManifestDigest: digest, Directory: destination, Identity: &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: "boot"}})
	if err == nil {
		t.Fatal("reused an existing runtime directory")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "existing runtime" {
		t.Fatalf("modified pre-existing runtime: %q %v", data, err)
	}
}

func TestRuntimeRejectsResourceRequestBeforeDiskPreparation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("runtime is Linux-only")
	}
	directory, digest := runtimeFixture(t)
	destination := filepath.Join(t.TempDir(), "new-runtime")
	_, err := StartRuntime(t.Context(), t.Context(), RuntimeConfig{VMMBinary: "/bin/false", ArtifactDirectory: directory, ManifestDigest: digest, Directory: destination, CPUs: 65, Identity: &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: "boot"}})
	if err == nil {
		t.Fatal("accepted excessive resources")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("invalid request prepared a disk directory: %v", err)
	}
}

func runtimeFixture(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	data := []byte("fixture payload, never a bootable image")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	for _, name := range []string{"kernel", "rootfs.raw"} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0400); err != nil {
			t.Fatal(err)
		}
	}
	var m templateartifact.Manifest
	m.SchemaVersion = 1
	m.Name = "fixture"
	m.Version = "test"
	m.Arch = "amd64"
	m.VMM.Kind = "cloud-hypervisor"
	m.VMM.TestedVersion = "test"
	m.Kernel = templateartifact.File{Name: "kernel", Size: int64(len(data)), SHA256: hash}
	m.RootFS.File = templateartifact.File{Name: "rootfs.raw", Size: int64(len(data)), SHA256: hash}
	m.RootFS.Format = "raw"
	m.RootFS.Filesystem = "ext4"
	m.Workspace.Filesystem = "ext4"
	m.Workspace.MinimumSizeBytes = 64 << 20
	m.GuestProtocol.Major = 1
	m.GuestAgent.Version = "test"
	m.GuestAgent.Capabilities = []string{"ready.v1"}
	m.WorkloadUser.Name = "isodo"
	m.WorkloadUser.UID = 1000
	m.WorkloadUser.GID = 1000
	m.MinimumMemoryMiB = 128
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), encoded, 0400); err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(encoded)
	return directory, hex.EncodeToString(sum[:])
}
