package orchestrator

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestT3PrepareNativeLibrary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		arch       string
		compressed bool
	}{
		{name: "compressed arm64", arch: "arm64", compressed: true},
		{name: "compressed x64", arch: "x64", compressed: true},
		{name: "uncompressed arm64", arch: "arm64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			archivePath := filepath.Join(root, "runtime bundle.tar.gz")
			runtimePath := filepath.Join(root, "runtime directory")
			libraryPath := "node_modules/@ff-labs/fff-bin-linux-" + tc.arch + "-gnu/libfff_c.so"
			library := []byte("native library fixture")
			content := library
			entryPath := "dist/" + libraryPath
			if tc.compressed {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				if _, err := writer.Write(library); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				content = compressed.Bytes()
				entryPath += ".gz"
			}
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tarWriter := tar.NewWriter(gz)
			if err := tarWriter.WriteHeader(&tar.Header{Name: entryPath, Mode: 0o644, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tarWriter.Write(content); err != nil {
				t.Fatal(err)
			}
			if err := tarWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "/bin/sh", "-c", t3PrepareScript, "isodo-t3-prepare", archivePath, runtimePath)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("prepare runtime: %v: %s", err, output)
			}
			actual, err := os.ReadFile(filepath.Join(runtimePath, libraryPath))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, library) {
				t.Fatalf("native library bytes = %q", actual)
			}
		})
	}
}
