package templatebuilder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

// SnapshotResources fixes the hardware captured by a golden runtime template.
type SnapshotResources struct {
	CPUs        uint32
	MemoryBytes uint64
}

func snapshotResources(m templateartifact.Manifest, resources SnapshotResources) (uint32, uint64, error) {
	if resources.MemoryBytes == 0 {
		resources.MemoryBytes = max(2<<30, uint64(m.MinimumMemoryMiB)<<20)
	}
	return runtimeResources(m, resources.CPUs, resources.MemoryBytes)
}

func runtimeResources(m templateartifact.Manifest, cpus uint32, memory uint64) (uint32, uint64, error) {
	if cpus == 0 {
		cpus = 2
		if m.RuntimeSnapshot != nil {
			cpus = m.RuntimeSnapshot.VCPU
		}
	}
	if memory == 0 {
		memory = uint64(m.MinimumMemoryMiB) << 20
		if m.RuntimeSnapshot != nil {
			memory = m.RuntimeSnapshot.MemoryBytes
		}
	}
	if cpus == 0 || cpus > 64 || memory < uint64(m.MinimumMemoryMiB)<<20 || memory > 4<<30 {
		return 0, 0, errors.New("runtime resources are outside supported limits")
	}
	if snapshot := m.RuntimeSnapshot; snapshot != nil && (cpus != snapshot.VCPU || memory != snapshot.MemoryBytes) {
		return 0, 0, errors.New("requested resources do not match runtime snapshot")
	}
	return cpus, memory, nil
}

func persistedDisks(directory string, m templateartifact.Manifest) ([]vmm.DiskConfig, error) {
	names, imageType := []string{"rootfs.raw", "workspace.raw"}, "Raw"
	if m.RuntimeSnapshot != nil {
		names, imageType = []string{"root.qcow2", "workspace.qcow2"}, "Qcow2"
	}
	disks := make([]vmm.DiskConfig, 0, len(names))
	for _, name := range names {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("persisted sandbox disk %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("persisted sandbox disk %s is not a regular file", name)
		}
		disks = append(disks, vmm.DiskConfig{Path: path, ImageType: imageType, BackingFiles: imageType == "Qcow2"})
	}
	return disks, nil
}
