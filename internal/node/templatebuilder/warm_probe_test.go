package templatebuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreConfigUsesPrivateOverlaysAndSockets(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.json")
	destination := filepath.Join(directory, "clone.json")
	original := []byte(`{"disks":[{"id":"root","path":"source-root","image_type":"Raw"},{"id":"workspace","path":"source-workspace","image_type":"Raw"}],"vsock":{"cid":3,"socket":"source-vsock"},"memory":{"size":536870912},"serial":{"mode":"File","file":"source-log"}}`)
	if err := os.WriteFile(source, original, 0400); err != nil {
		t.Fatal(err)
	}
	if err := rewriteSnapshotConfig(source, destination, "clone-root", "clone-workspace", "clone-vsock", "clone-log"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != string(original) {
		t.Fatal("rewrote the shared snapshot config")
	}
	data, err = os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Disks []struct {
			ID, Path, ImageType string
			BackingFiles        bool `json:"backing_files"`
		} `json:"disks"`
		Vsock struct {
			CID    int
			Socket string
		} `json:"vsock"`
	}
	// Use a generic map for the explicitly snake-cased image_type field.
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	disks := config["disks"].([]any)
	root := disks[0].(map[string]any)
	if root["path"] != "clone-root" || root["id"] != "root" || root["image_type"] != "Qcow2" || root["backing_files"] != true {
		t.Fatalf("invalid clone disk: %+v", root)
	}
	if err = json.Unmarshal(data, &value); err != nil || value.Vsock.Socket != "clone-vsock" || value.Vsock.CID != 3 {
		t.Fatalf("invalid clone vsock: %+v %v", value, err)
	}
	if config["memory"].(map[string]any)["size"] != float64(512<<20) {
		t.Fatal("changed snapshot memory layout")
	}
}
func TestPrivateSnapshotMappingEvidence(t *testing.T) {
	maps := `1000-3000 rw-p 00000000 00:00 1 /tmp/snapshot/memory-ranges
4000-5000 r--p 00000000 00:00 1 /tmp/snapshot/memory-ranges
5000-6000 rw-s 00000000 00:00 1 /tmp/snapshot/memory-ranges
7000-8000 rw-p 00000000 00:00 1 /memfd:guest
`
	if got := snapshotMappingBytes(maps); got != 8192 {
		t.Fatalf("private mapping bytes=%d", got)
	}
}
