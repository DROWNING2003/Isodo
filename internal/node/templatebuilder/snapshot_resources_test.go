package templatebuilder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

func TestRuntimeResourcesUseSnapshotDefaults(t *testing.T) {
	m := templateartifact.Manifest{MinimumMemoryMiB: 512, RuntimeSnapshot: &templateartifact.RuntimeSnapshot{VCPU: 2, MemoryBytes: 2 << 30}}
	cpus, memory, err := runtimeResources(m, 0, 0)
	if err != nil || cpus != 2 || memory != 2<<30 {
		t.Fatalf("resources=%d/%d err=%v", cpus, memory, err)
	}
	if _, _, err := runtimeResources(m, 2, 4<<30); err == nil {
		t.Fatal("accepted incompatible snapshot memory")
	}
}

func TestSnapshotResourcesMatchCloudDefaults(t *testing.T) {
	m := templateartifact.Manifest{MinimumMemoryMiB: 512}
	cpus, memory, err := snapshotResources(m, SnapshotResources{})
	if err != nil || cpus != 2 || memory != 2<<30 {
		t.Fatalf("resources=%d/%d err=%v", cpus, memory, err)
	}
	if _, _, err := snapshotResources(m, SnapshotResources{MemoryBytes: 128 << 20}); err == nil {
		t.Fatal("accepted memory below template minimum")
	}
}

func TestPersistedSnapshotDisksKeepPrivateOverlays(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"root.qcow2", "workspace.qcow2"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("existing private data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	disks, err := persistedDisks(directory, templateartifact.Manifest{RuntimeSnapshot: &templateartifact.RuntimeSnapshot{}})
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"root.qcow2", "workspace.qcow2"} {
		if disks[i].Path != filepath.Join(directory, name) || disks[i].ImageType != "Qcow2" || !disks[i].BackingFiles {
			t.Fatalf("disk=%+v", disks[i])
		}
		data, err := os.ReadFile(disks[i].Path)
		if err != nil || string(data) != "existing private data" {
			t.Fatalf("disk changed: %q %v", data, err)
		}
	}
	if err := os.Remove(filepath.Join(directory, "workspace.qcow2")); err != nil {
		t.Fatal(err)
	}
	if _, err := persistedDisks(directory, templateartifact.Manifest{RuntimeSnapshot: &templateartifact.RuntimeSnapshot{}}); err == nil {
		t.Fatal("accepted missing overlay")
	}
}
