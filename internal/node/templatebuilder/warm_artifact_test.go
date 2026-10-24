package templatebuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

func TestNormalizedSnapshotConfigPinsArtifactPayloads(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.json")
	destination := filepath.Join(directory, "vm-config.json")
	original := []byte(`{"disks":[{"path":"old-root","readonly":false,"image_type":"Qcow2","backing_files":true},{"path":"old-workspace","readonly":false,"image_type":"Qcow2","backing_files":true}],"vsock":{"cid":7,"socket":"old-vsock"},"payload":{"kernel":"old-kernel","initramfs":"old-initramfs","extra":"preserve"},"serial":{"mode":"File","file":"old.log"},"memory":{"size":536870912}}`)
	if err := os.WriteFile(source, original, 0400); err != nil {
		t.Fatal(err)
	}
	manifest := templateartifact.Manifest{
		Kernel:    templateartifact.File{Name: "vmlinux"},
		RootFS:    templateartifact.RootFS{File: templateartifact.File{Name: "rootfs.raw"}},
		Initramfs: new(templateartifact.File),
	}
	manifest.Initramfs.Name = "initramfs.cpio"
	if err := normalizedSnapshotConfig(source, destination, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	disks := config["disks"].([]any)
	for i, want := range []string{"rootfs.raw", "workspace.raw"} {
		disk := disks[i].(map[string]any)
		if disk["path"] != want || disk["readonly"] != true || disk["image_type"] != "Raw" || disk["backing_files"] != false {
			t.Fatalf("disk %d was not normalized: %+v", i, disk)
		}
	}
	if got := config["vsock"].(map[string]any)["socket"]; got != "vsock" {
		t.Fatalf("socket=%v, want vsock", got)
	}
	payload := config["payload"].(map[string]any)
	if payload["kernel"] != "vmlinux" || payload["initramfs"] != "initramfs.cpio" || payload["extra"] != "preserve" {
		t.Fatalf("payload was not normalized: %+v", payload)
	}
	if serial := config["serial"].(map[string]any); len(serial) != 1 || serial["mode"] != "Off" {
		t.Fatalf("serial=%+v, want only Off", serial)
	}
	if mode := os.FileMode(0o777) & mustStat(t, destination).Mode().Perm(); mode != 0400 {
		t.Fatalf("destination permissions=%o, want 400", mode)
	}
}

func TestNormalizedSnapshotConfigRemovesStaleInitramfs(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.json")
	destination := filepath.Join(directory, "vm-config.json")
	if err := os.WriteFile(source, []byte(`{"disks":[{},{}],"vsock":{},"payload":{"initramfs":"stale"}}`), 0400); err != nil {
		t.Fatal(err)
	}
	manifest := templateartifact.Manifest{Kernel: templateartifact.File{Name: "vmlinux"}}
	if err := normalizedSnapshotConfig(source, destination, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["payload"].(map[string]any)["initramfs"]; ok {
		t.Fatal("stale initramfs remained in normalized payload")
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
