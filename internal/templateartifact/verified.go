package templateartifact

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Artifact is an immutable, process-local verification receipt. Its zero value
// is invalid; only VerifyArtifact constructs a usable receipt.
type Artifact struct {
	directory, digest string
	manifest          Manifest
	directoryInfo     os.FileInfo
	files             map[string]os.FileInfo
}

func VerifyArtifact(directory, digest string) (*Artifact, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	dirInfo, files, err := artifactMetadata(absolute)
	if err != nil {
		return nil, err
	}
	manifest, err := Verify(absolute, digest)
	if err != nil {
		return nil, err
	}
	artifact := &Artifact{directory: absolute, digest: digest, manifest: manifest, directoryInfo: dirInfo, files: files}
	if err = artifact.Check(); err != nil {
		return nil, err
	}
	return artifact, nil
}
func artifactMetadata(directory string) (os.FileInfo, map[string]os.FileInfo, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, errors.New("verified artifact directory must not be a symlink")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > 64 {
		return nil, nil, errors.New("too many files in immutable artifact directory")
	}
	files := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		metadata, err := entry.Info()
		if err != nil {
			return nil, nil, err
		}
		if !metadata.Mode().IsRegular() || metadata.Mode().Perm()&0222 != 0 {
			return nil, nil, errors.New("verified artifact files must be regular and read-only")
		}
		files[entry.Name()] = metadata
	}
	return info, files, nil
}
func sameArtifactFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

// Check performs metadata checks, not a second full hash. Host-owned cache files
// must stay immutable. A changed receipt must be replaced by full verification.
func (a *Artifact) Check() error {
	if a == nil || a.directory == "" || a.digest == "" || a.files == nil {
		return errors.New("artifact verification receipt is not initialized")
	}
	directory, files, err := artifactMetadata(a.directory)
	if err != nil {
		return err
	}
	if !sameArtifactFile(a.directoryInfo, directory) || len(files) != len(a.files) {
		return errors.New("artifact directory changed after verification")
	}
	for name, metadata := range a.files {
		if !sameArtifactFile(metadata, files[name]) {
			return errors.New("artifact file changed after verification")
		}
	}
	return nil
}
func (a *Artifact) Directory() string { return a.directory }
func (a *Artifact) Digest() string    { return a.digest }
func (a *Artifact) Manifest() Manifest {
	m := a.manifest
	m.GuestAgent.Capabilities = slices.Clone(m.GuestAgent.Capabilities)
	if m.Initramfs != nil {
		m.Initramfs = new(*m.Initramfs)
	}
	if m.KernelConfig != nil {
		m.KernelConfig = new(*m.KernelConfig)
	}
	if m.RuntimeSnapshot != nil {
		m.RuntimeSnapshot = new(*m.RuntimeSnapshot)
	}
	return m
}
