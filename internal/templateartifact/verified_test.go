package templateartifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readonlyFixture(t *testing.T) (string, string) {
	t.Helper()
	directory, m := fixture(t)
	digest := saveManifest(t, directory, m)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err = os.Chmod(filepath.Join(directory, entry.Name()), 0400); err != nil {
			t.Fatal(err)
		}
	}
	return directory, digest
}
func TestVerificationReceiptDetectsChanges(t *testing.T) {
	directory, digest := readonlyFixture(t)
	artifact, err := VerifyArtifact(directory, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err = artifact.Check(); err != nil {
		t.Fatal(err)
	}
	manifest := artifact.Manifest()
	manifest.GuestAgent.Capabilities[0] = "modified"
	if artifact.Manifest().GuestAgent.Capabilities[0] == "modified" {
		t.Fatal("caller mutated verified manifest")
	}
	path := filepath.Join(directory, manifest.RootFS.Name)
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = artifact.Check(); err == nil {
		t.Fatal("writable cache file retained receipt")
	}
	if err = os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	changed := time.Now().Add(time.Hour)
	if err = os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	if err = artifact.Check(); err == nil {
		t.Fatal("modified cache stamp retained receipt")
	}
}
func TestCachedReceiptAvoidsNetworkAndRejectsDigestAliases(t *testing.T) {
	directory, digest := readonlyFixture(t)
	root := t.TempDir()
	key := filepath.Join(root, digest)
	if err := os.Rename(directory, key); err != nil {
		t.Fatal(err)
	}
	artifact, err := VerifyArtifact(key, digest)
	if err != nil {
		t.Fatal(err)
	}
	// No S3 client is installed: a valid hot receipt avoids all network work.
	store := &S3Store{verified: map[string]*Artifact{key: artifact}}
	got, err := store.FetchVerified(context.Background(), root, digest)
	if err != nil || got != artifact {
		t.Fatalf("hot receipt: %v", err)
	}
	if _, err = store.FetchVerified(context.Background(), root, "folder/../"+digest); err == nil {
		t.Fatal("invalid digest aliased a verified cache entry")
	}
}

func TestRuntimeSnapshotRequiresCompatibilityAndFixedPayloads(t *testing.T) {
	_, manifest := fixture(t)
	file := func(name string, size int64) File {
		return File{Name: name, Size: size, SHA256: strings.Repeat("a", 64)}
	}
	manifest.KernelConfig = new(file("kernel.config", 128))
	manifest.KernelVersion = "test-kernel"
	manifest.RuntimeSnapshot = &RuntimeSnapshot{SchemaVersion: 1, Config: file("vm-config.json", 128), State: file("vm-state.json", 128), Memory: file("memory-ranges", 512<<20), Workspace: file("workspace.raw", manifest.Workspace.MinimumSizeBytes), VCPU: 2, MemoryBytes: 512 << 20, CPUSignature: strings.Repeat("b", 64), VMMDigest: strings.Repeat("c", 64), SourceDigest: strings.Repeat("d", 64)}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	manifest.RuntimeSnapshot.Memory.Size--
	if err := manifest.Validate(); err == nil {
		t.Fatal("snapshot memory size does not match its resources")
	}
	manifest.RuntimeSnapshot.Memory.Size++
	manifest.RuntimeSnapshot.CPUSignature = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("snapshot lacks CPU compatibility signature")
	}
}
