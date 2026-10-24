package templatebuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

// BuildWarmArtifact freezes an unbound Guest into a self-contained immutable
// artifact. It never publishes the Registry or overwrites an existing directory.
func BuildWarmArtifact(ctx context.Context, binary, source, digest, destination string) (_ string, err error) {
	source, err = filepath.Abs(source)
	if err != nil {
		return "", err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	rel, e := filepath.Rel(source, destination)
	if e != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return "", errors.New("warm output must be outside the source artifact")
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()
	frozen, err := freezeTemplate(ctx, binary, source, digest, filepath.Dir(destination))
	if err != nil {
		return "", err
	}
	defer frozen.close()
	m := frozen.manifest
	m.RootFS.Name = "rootfs.raw"
	if err = os.Rename(frozen.config.Disks[0].Path, filepath.Join(destination, m.RootFS.Name)); err != nil {
		return "", err
	}
	if err = os.Rename(frozen.config.Disks[1].Path, filepath.Join(destination, "workspace.raw")); err != nil {
		return "", err
	}
	for _, pair := range [][2]string{{"state.json", "vm-state.json"}, {"memory-ranges", "memory-ranges"}} {
		if err = os.Rename(filepath.Join(frozen.snapshot, pair[0]), filepath.Join(destination, pair[1])); err != nil {
			return "", err
		}
	}
	copyPayload := func(input templateartifact.File, name string) (templateartifact.File, error) {
		if err := copySmallImmutable(filepath.Join(source, input.Name), filepath.Join(destination, name)); err != nil {
			return templateartifact.File{}, err
		}
		input.Name = name
		return input, nil
	}
	m.Kernel, err = copyPayload(frozen.manifest.Kernel, "vmlinux")
	if err != nil {
		return "", err
	}
	if original := frozen.manifest.Initramfs; original != nil {
		value, e := copyPayload(*original, "initramfs.cpio")
		if e != nil {
			return "", e
		}
		m.Initramfs = new(value)
	}
	if original := frozen.manifest.KernelConfig; original != nil {
		value, e := copyPayload(*original, "kernel.config")
		if e != nil {
			return "", e
		}
		m.KernelConfig = new(value)
	}
	if err = normalizedSnapshotConfig(filepath.Join(frozen.snapshot, "config.json"), filepath.Join(destination, "vm-config.json"), m); err != nil {
		return "", err
	}
	root, err := describeArtifactFile(destination, m.RootFS.Name)
	if err != nil {
		return "", err
	}
	m.RootFS.File = root
	snapshot := &templateartifact.RuntimeSnapshot{SchemaVersion: 1, VCPU: frozen.config.CPUs.BootVCPUs, MemoryBytes: frozen.config.Memory.Size, SourceDigest: digest}
	snapshot.CPUSignature, err = cpuSignature()
	if err != nil {
		return "", err
	}
	snapshot.VMMDigest, err = vmmDigest(binary)
	if err != nil {
		return "", err
	}
	snapshot.Config, err = describeArtifactFile(destination, "vm-config.json")
	if err != nil {
		return "", err
	}
	snapshot.State, err = describeArtifactFile(destination, "vm-state.json")
	if err != nil {
		return "", err
	}
	snapshot.Memory, err = describeArtifactFile(destination, "memory-ranges")
	if err != nil {
		return "", err
	}
	snapshot.Workspace, err = describeArtifactFile(destination, "workspace.raw")
	if err != nil {
		return "", err
	}
	m.RuntimeSnapshot = snapshot
	if err = m.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	if err = os.WriteFile(filepath.Join(destination, "manifest.json"), encoded, 0400); err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	publishedDigest := hex.EncodeToString(sum[:])
	if _, err = templateartifact.Verify(destination, publishedDigest); err != nil {
		return "", err
	}
	manifestFile, e := os.Open(filepath.Join(destination, "manifest.json"))
	if e != nil {
		return "", e
	}
	e = errors.Join(manifestFile.Sync(), manifestFile.Close())
	if e != nil {
		return "", e
	}
	if err = syncArtifactDirectory(destination); err != nil {
		return "", err
	}
	complete = true
	return publishedDigest, nil
}
func copySmallImmutable(source, destination string) error {
	if info, err := os.Stat(source); err == nil && info.Mode().Perm()&0222 == 0 {
		if err := os.Link(source, destination); err == nil {
			return nil
		}
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Sync(), output.Close())
}
func describeArtifactFile(directory, name string) (templateartifact.File, error) {
	path := filepath.Join(directory, name)
	info, err := os.Stat(path)
	if err != nil {
		return templateartifact.File{}, err
	}
	if !info.Mode().IsRegular() {
		return templateartifact.File{}, errors.New("payload must be regular")
	}
	if err = os.Chmod(path, 0400); err != nil {
		return templateartifact.File{}, err
	}
	digest, err := hashFile(path)
	if err != nil {
		return templateartifact.File{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return templateartifact.File{}, err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return templateartifact.File{}, err
	}
	return templateartifact.File{Name: name, Size: info.Size(), SHA256: digest}, nil
}
func syncArtifactDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
func normalizedSnapshotConfig(source, destination string, m templateartifact.Manifest) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	disks, ok := config["disks"].([]any)
	if !ok || len(disks) != 2 {
		return errors.New("snapshot disk layout is unsupported")
	}
	for i, name := range []string{m.RootFS.Name, "workspace.raw"} {
		disk, ok := disks[i].(map[string]any)
		if !ok {
			return errors.New("snapshot disk config is invalid")
		}
		disk["path"] = name
		disk["readonly"] = true
		disk["image_type"] = "Raw"
		disk["backing_files"] = false
	}
	vsock, ok := config["vsock"].(map[string]any)
	if !ok {
		return errors.New("snapshot has no vsock device")
	}
	vsock["socket"] = "vsock"
	payload, ok := config["payload"].(map[string]any)
	if !ok {
		return errors.New("snapshot has no kernel payload")
	}
	payload["kernel"] = m.Kernel.Name
	if m.Initramfs != nil {
		payload["initramfs"] = m.Initramfs.Name
	} else {
		delete(payload, "initramfs")
	}
	config["serial"] = map[string]any{"mode": "Off"}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, encoded, 0400)
}
