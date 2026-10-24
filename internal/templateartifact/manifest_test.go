package templateartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, Manifest) {
	t.Helper()
	directory := t.TempDir()
	var m Manifest
	m.SchemaVersion = 1
	m.Name = "linux-base"
	m.Version = "test"
	m.Arch = "amd64"
	m.VMM.Kind = "cloud-hypervisor"
	m.VMM.TestedVersion = "v54.0"
	for _, entry := range []struct {
		name   string
		target *File
	}{{"vmlinux", &m.Kernel}, {"rootfs.raw", &m.RootFS.File}} {
		content := []byte("integrity fixture " + entry.name)
		sum := sha256.Sum256(content)
		if err := os.WriteFile(filepath.Join(directory, entry.name), content, 0600); err != nil {
			t.Fatal(err)
		}
		*entry.target = File{Name: entry.name, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	}
	m.RootFS.Format = "raw"
	m.RootFS.Filesystem = "ext4"
	m.Workspace.Filesystem = "ext4"
	m.Workspace.MinimumSizeBytes = 4096
	m.GuestProtocol.Major = 1
	m.GuestAgent.Version = "test"
	m.GuestAgent.Capabilities = []string{"ready.v1"}
	m.WorkloadUser.Name = "isodo"
	m.WorkloadUser.UID = 1000
	m.WorkloadUser.GID = 1000
	m.MinimumMemoryMiB = 128
	return directory, m
}
func saveManifest(t *testing.T, directory string, m Manifest) string {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func TestVerifyManifestIntegrity(t *testing.T) {
	directory, m := fixture(t)
	digest := saveManifest(t, directory, m)
	if _, err := Verify(directory, digest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, m.RootFS.Name), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(directory, digest); err == nil {
		t.Fatal("tampered rootfs accepted")
	}
}
func TestVerifyRejectsEscapesAndSymlinks(t *testing.T) {
	t.Run("path escape", func(t *testing.T) {
		directory, m := fixture(t)
		m.Kernel.Name = "../vmlinux"
		digest := saveManifest(t, directory, m)
		if _, err := Verify(directory, digest); err == nil {
			t.Fatal("path escape accepted")
		}
	})
	t.Run("artifact symlink", func(t *testing.T) {
		directory, m := fixture(t)
		content, err := os.ReadFile(filepath.Join(directory, m.Kernel.Name))
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "kernel")
		if err = os.WriteFile(outside, content, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(filepath.Join(directory, m.Kernel.Name)); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(outside, filepath.Join(directory, m.Kernel.Name)); err != nil {
			t.Fatal(err)
		}
		digest := saveManifest(t, directory, m)
		if _, err = Verify(directory, digest); err == nil {
			t.Fatal("artifact symlink accepted")
		}
	})
	t.Run("manifest symlink", func(t *testing.T) {
		directory, m := fixture(t)
		digest := saveManifest(t, directory, m)
		outside := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.Rename(filepath.Join(directory, "manifest.json"), outside); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(directory, "manifest.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(directory, digest); err == nil {
			t.Fatal("manifest symlink accepted")
		}
	})
}

func TestVerifyOptionalInitramfs(t *testing.T) {
	directory, m := fixture(t)
	data := []byte("initramfs integrity fixture")
	sum := sha256.Sum256(data)
	m.Initramfs = &File{Name: "initramfs.cpio", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	if err := os.WriteFile(filepath.Join(directory, m.Initramfs.Name), data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := saveManifest(t, directory, m)
	if _, err := Verify(directory, digest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, m.Initramfs.Name), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(directory, digest); err == nil {
		t.Fatal("corrupt initramfs accepted")
	}
}

func TestVerifyKernelConfigurationArtifact(t *testing.T) {
	directory, m := fixture(t)
	data := []byte("CONFIG_VMGENID=m\n")
	sum := sha256.Sum256(data)
	m.KernelVersion = "6.8-fixture"
	m.KernelConfig = &File{Name: "kernel.config", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	if err := os.WriteFile(filepath.Join(directory, m.KernelConfig.Name), data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := saveManifest(t, directory, m)
	if _, err := Verify(directory, digest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, m.KernelConfig.Name), []byte("CONFIG_VMGENID=n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(directory, digest); err == nil {
		t.Fatal("accepted a corrupted kernel configuration")
	}
}
