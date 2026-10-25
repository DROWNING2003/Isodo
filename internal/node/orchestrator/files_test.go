package orchestrator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net"
	"os"
	"path/filepath"
	"testing"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	guestagent "github.com/DROWNING2003/Isodo/internal/guest/agent"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFilesBackendUsesGuestAtomicCommitAndWorkspaceBoundary(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("ISODO_WORKSPACE", workspace)
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox-files", OwnershipGeneration: 3, BootId: "boot", GuestEpoch: "epoch"}
	guest := guestagent.NewServer(guestagent.Identity{SandboxID: identity.SandboxId, OwnershipGeneration: identity.OwnershipGeneration, BootID: identity.BootId, GuestEpoch: identity.GuestEpoch})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(server, guest)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	backend := guestFileBackend{open: func(_ context.Context, target *nodev1.SandboxTarget) (fileGuest, error) {
		if target.GetOwnershipGeneration() != 3 || target.GetRevision() != 9 {
			return fileGuest{}, status.Error(codes.FailedPrecondition, "stale target")
		}
		connection, err := guesttransport.NewTCPClientConn(listener.Addr().String())
		if err != nil {
			return fileGuest{}, err
		}
		return fileGuest{client: guestv1.NewGuestServiceClient(connection), identity: identity, close: func() { _ = connection.Close() }, check: func() error { return nil }}, nil
	}}
	target := &nodev1.SandboxTarget{SandboxId: identity.SandboxId, OwnershipGeneration: 3, Revision: 9}
	_, err = backend.Mkdir(t.Context(), &nodev1.RuntimeDirectoryRequest{Target: target, Path: "/workspace/.codex", Parents: true, RequestId: "mkdir-config"})
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Repeat([]byte{0, 255, 128, 10, 1}, 60000)
	sum := sha256.Sum256(original)
	header := &nodev1.RuntimeWriteHeader{Target: target, Path: "/workspace/.codex/config.toml", SizeBytes: uint64(len(original)), Sha256: sum[:], Overwrite: true, RequestId: "write-config"}
	result, err := backend.Write(t.Context(), header, bytes.NewReader(original))
	if err != nil || !bytes.Equal(result.GetSha256(), sum[:]) {
		t.Fatalf("write=%v", err)
	}
	var downloaded bytes.Buffer
	err = backend.Read(t.Context(), &nodev1.RuntimeReadRequest{Target: target, Path: header.Path}, func(frame *guestv1.ReadFileFrame) error {
		if chunk := frame.GetChunk(); chunk != nil {
			_, _ = downloaded.Write(chunk.Data)
		}
		return nil
	})
	if err != nil || !bytes.Equal(downloaded.Bytes(), original) {
		t.Fatalf("binary read=%v", err)
	}
	header.RequestId = "write-conflict"
	header.Overwrite = false
	if _, err := backend.Write(t.Context(), header, bytes.NewReader(original)); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("overwrite=false=%v", err)
	}
	header.RequestId = "bad-digest"
	header.Overwrite = true
	header.Sha256 = make([]byte, 32)
	if _, err := backend.Write(t.Context(), header, bytes.NewReader(original)); status.Code(err) != codes.DataLoss {
		t.Fatalf("digest mismatch=%v", err)
	}
	actual, err := os.ReadFile(filepath.Join(workspace, ".codex", "config.toml"))
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatal("failed upload changed committed file")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	header.RequestId = "escape"
	header.Path = "/workspace/escape/config.toml"
	header.Sha256 = sum[:]
	if _, err := backend.Write(t.Context(), header, bytes.NewReader(original)); err == nil {
		t.Fatal("symlink escaped workspace")
	}
	if _, err := os.Stat(filepath.Join(outside, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("outside workspace was modified")
	}
}

func TestFileTargetRejectsStoppedOrSupersededInstance(t *testing.T) {
	target := &nodev1.SandboxTarget{SandboxId: "sandbox", OwnershipGeneration: 3, Revision: 9}
	base := sandboxRecord{SandboxID: "sandbox", Generation: 3, Revision: 9, State: "running", BootID: "boot", GuestEpoch: "epoch"}
	if err := checkFileRecord(base, true, false, target); err != nil {
		t.Fatal(err)
	}
	stale := base
	stale.Revision = 10
	if err := checkFileRecord(stale, true, false, target); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("accepted superseded file request")
	}
	stale = base
	stale.State = "stopped"
	if err := checkFileRecord(stale, true, false, target); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("accepted stopped Guest")
	}
	stale = base
	stale.Generation = 4
	if err := checkFileRecord(stale, true, false, target); status.Code(err) != codes.NotFound {
		t.Fatal("accepted foreign generation")
	}
}
