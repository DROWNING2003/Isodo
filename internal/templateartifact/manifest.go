// Package templateartifact verifies immutable, host-local template artifacts.
package templateartifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var filenamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

type File struct {
	Name   string `json:"file"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
}
type RootFS struct {
	File
	Format      string `json:"format"`
	Filesystem  string `json:"filesystem"`
	Partitioned bool   `json:"partitioned"`
}
type RuntimeSnapshot struct {
	SchemaVersion int    `json:"schema_version"`
	Config        File   `json:"config"`
	State         File   `json:"state"`
	Memory        File   `json:"memory"`
	Workspace     File   `json:"workspace"`
	VCPU          uint32 `json:"vcpu"`
	MemoryBytes   uint64 `json:"memory_bytes"`
	CPUSignature  string `json:"cpu_signature"`
	VMMDigest     string `json:"vmm_digest"`
	SourceDigest  string `json:"source_digest"`
}
type Manifest struct {
	RuntimeSnapshot *RuntimeSnapshot `json:"runtime_snapshot,omitempty"`
	SchemaVersion   int              `json:"schema_version"`
	Name            string           `json:"name"`
	Version         string           `json:"version"`
	Arch            string           `json:"arch"`
	VMM             struct {
		Kind          string `json:"kind"`
		TestedVersion string `json:"tested_version"`
	} `json:"vmm"`
	Kernel        File   `json:"kernel"`
	Initramfs     *File  `json:"initramfs,omitempty"`
	KernelConfig  *File  `json:"kernel_config,omitempty"`
	KernelVersion string `json:"kernel_version,omitempty"`
	RootFS        RootFS `json:"rootfs"`
	Workspace     struct {
		Filesystem       string `json:"filesystem"`
		MinimumSizeBytes int64  `json:"minimum_size_bytes"`
	} `json:"workspace"`
	GuestProtocol struct {
		Major        uint32 `json:"major"`
		MinimumMinor uint32 `json:"minimum_minor"`
	} `json:"guest_protocol"`
	GuestAgent struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	} `json:"guest_agent"`
	WorkloadUser struct {
		Name string `json:"name"`
		UID  uint32 `json:"uid"`
		GID  uint32 `json:"gid"`
	} `json:"workload_user"`
	MinimumMemoryMiB int `json:"minimum_memory_mib"`
}

// Verify checks exact manifest bytes and every referenced file. Files must be
// flat, regular, non-symlink files; callers retain the read-only directory.
func Verify(directory, expectedDigest string) (Manifest, error) {
	var m Manifest
	if !digestPattern.MatchString(expectedDigest) {
		return m, errors.New("invalid manifest digest")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return m, err
	}
	if !info.IsDir() {
		return m, errors.New("artifact directory must not be a symlink")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return m, err
	}
	defer root.Close()
	f, err := openRegular(root, "manifest.json")
	if err != nil {
		return m, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return m, err
	}
	if len(data) > 64<<10 {
		return m, errors.New("manifest exceeds 64 KiB")
	}
	m, err = ParseManifest(data, expectedDigest)
	if err != nil {
		return m, err
	}
	for _, artifact := range m.Files() {
		if artifact.Name == "manifest.json" {
			return m, errors.New("artifact cannot reference the manifest")
		}
		file, err := openRegular(root, artifact.Name)
		if err != nil {
			return m, err
		}
		hash := sha256.New()
		size, hashErr := io.Copy(hash, io.LimitReader(file, artifact.Size+1))
		closeErr := file.Close()
		if err = errors.Join(hashErr, closeErr); err != nil {
			return m, err
		}
		if size != artifact.Size || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
			return m, fmt.Errorf("artifact %s size/hash mismatch", artifact.Name)
		}
	}
	return m, nil
}

// ParseManifest authenticates manifest bytes before any referenced path is used.
func ParseManifest(data []byte, expectedDigest string) (Manifest, error) {
	var m Manifest
	if !digestPattern.MatchString(expectedDigest) || len(data) > 64<<10 {
		return m, errors.New("invalid manifest size or digest")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expectedDigest {
		return m, errors.New("manifest digest mismatch")
	}
	// Re-read from fixed bytes, rejecting unknown fields and trailing JSON.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, fmt.Errorf("decode manifest: %w", err)
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return m, errors.New("manifest contains trailing JSON")
	}
	if err := m.Validate(); err != nil {
		return m, err
	}

	return m, nil
}
func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 || m.Arch != "amd64" || m.VMM.Kind != "cloud-hypervisor" || m.VMM.TestedVersion == "" || m.Name == "" || m.Version == "" {
		return errors.New("unsupported or incomplete manifest identity")
	}
	seen := make(map[string]bool)
	var total int64
	for _, f := range m.Files() {
		if !filenamePattern.MatchString(f.Name) || f.Name == "manifest.json" || seen[f.Name] || f.Size <= 0 || f.Size > 16<<30 || !digestPattern.MatchString(f.SHA256) {
			return errors.New("invalid or duplicate artifact filename, size or digest")
		}
		seen[f.Name] = true
		total += f.Size
		if total > 16<<30 {
			return errors.New("template artifacts exceed the 16 GiB limit")
		}
	}
	if snapshot := m.RuntimeSnapshot; snapshot != nil {
		if snapshot.SchemaVersion != 1 || m.KernelConfig == nil || m.KernelVersion == "" || snapshot.VCPU == 0 || snapshot.VCPU > 64 || snapshot.MemoryBytes < 128<<20 || snapshot.MemoryBytes < uint64(m.MinimumMemoryMiB)<<20 || snapshot.MemoryBytes > 4<<30 || snapshot.Memory.Size != int64(snapshot.MemoryBytes) || snapshot.Workspace.Size != m.Workspace.MinimumSizeBytes || snapshot.Config.Size > 1<<20 || snapshot.State.Size > 64<<20 || !digestPattern.MatchString(snapshot.CPUSignature) || !digestPattern.MatchString(snapshot.VMMDigest) || !digestPattern.MatchString(snapshot.SourceDigest) {
			return errors.New("unsupported or incomplete runtime snapshot baseline")
		}
	}
	if m.RootFS.Format != "raw" || m.RootFS.Filesystem != "ext4" || m.RootFS.Partitioned || m.Workspace.Filesystem != "ext4" || m.Workspace.MinimumSizeBytes <= 0 {
		return errors.New("unsupported disk layout")
	}
	if m.GuestProtocol.Major != 1 || m.GuestAgent.Version == "" || !slices.Contains(m.GuestAgent.Capabilities, "ready.v1") || m.WorkloadUser.Name != "isodo" || m.WorkloadUser.UID != 1000 || m.WorkloadUser.GID != 1000 || m.MinimumMemoryMiB < 128 {
		return errors.New("invalid Guest Agent/workload baseline")
	}
	return nil
}
func openRegular(root *os.Root, name string) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	current, err := f.Stat()
	if err != nil || !os.SameFile(info, current) {
		f.Close()
		return nil, errors.New("artifact changed while opening")
	}
	return f, nil
}

// Files returns all immutable payload files named by the manifest.
func (m Manifest) Files() []File {
	files := []File{m.Kernel, m.RootFS.File}
	if m.Initramfs != nil {
		files = append(files, *m.Initramfs)
	}
	if m.KernelConfig != nil {
		files = append(files, *m.KernelConfig)
	}
	if snapshot := m.RuntimeSnapshot; snapshot != nil {
		files = append(files, snapshot.Config, snapshot.State, snapshot.Memory, snapshot.Workspace)
	}
	return files
}
