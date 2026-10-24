package agent

import (
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGuestFilesOverGRPC(t *testing.T) {
	workspace := t.TempDir()
	identity := &guestv1.RuntimeIdentity{
		SandboxId:           "sandbox-files-test",
		OwnershipGeneration: 3,
		BootId:              "boot-files-test",
		GuestEpoch:          "epoch-files-test",
	}
	server := NewServer(Identity{
		SandboxID:           identity.GetSandboxId(),
		OwnershipGeneration: identity.GetOwnershipGeneration(),
		BootID:              identity.GetBootId(),
		GuestEpoch:          identity.GetGuestEpoch(),
	})
	server.workspace = workspace
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	guestv1.RegisterGuestServiceServer(grpcServer, server)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
		select {
		case err := <-serveErrors:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("serve Guest gRPC: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Guest gRPC server did not stop")
		}
	})
	conn, err := guesttransport.NewTCPClientConn(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := guestv1.NewGuestServiceClient(conn)
	ctx := t.Context()

	if _, err := client.MakeDirectory(ctx, &guestv1.MakeDirectoryRequest{Identity: identity, RequestId: "mkdir-1", Path: "/workspace/project"}); err != nil {
		t.Fatalf("create workspace directory: %v", err)
	}
	content := []byte{0, 1, 2, 0xfe, 0xff, '\n'}
	digest := sha256.Sum256(content)
	write, err := client.WriteFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := write.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Header{Header: &guestv1.WriteFileHeader{
		Identity: identity, RequestId: "write-1", Path: "/workspace/project/data.bin", SizeBytes: uint64(len(content)), Sha256: digest[:],
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := write.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Chunk{Chunk: &guestv1.FileChunk{Data: content}}}); err != nil {
		t.Fatal(err)
	}
	writeResult, err := write.CloseAndRecv()
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	if writeResult.GetFile().GetSizeBytes() != uint64(len(content)) || !equalBytes(writeResult.GetSha256(), digest[:]) {
		t.Fatalf("unexpected write result: %+v", writeResult)
	}

	read, err := client.ReadFile(ctx, &guestv1.ReadFileRequest{Identity: identity, Path: "/workspace/project/data.bin"})
	if err != nil {
		t.Fatal(err)
	}
	var readBytes []byte
	for {
		frame, err := read.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if chunk := frame.GetChunk(); chunk != nil {
			if chunk.GetOffset() != uint64(len(readBytes)) {
				t.Fatalf("unexpected chunk offset %d", chunk.GetOffset())
			}
			readBytes = append(readBytes, chunk.GetData()...)
		}
	}
	if !equalBytes(readBytes, content) {
		t.Fatalf("read bytes %v, want %v", readBytes, content)
	}

	listed, err := client.ListDirectory(ctx, &guestv1.ListDirectoryRequest{Identity: identity, Path: "/workspace/project", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetEntries()) != 1 || listed.GetEntries()[0].GetName() != "data.bin" || listed.GetNextPageToken() != "" {
		t.Fatalf("unexpected directory listing: %+v", listed)
	}

	if _, err := client.Stat(ctx, &guestv1.StatRequest{Identity: identity, Path: "/workspace/../secret"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("path traversal error = %v, want InvalidArgument", err)
	}
	if err := os.Symlink("../secret", filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	symlink, err := client.Stat(ctx, &guestv1.StatRequest{Identity: identity, Path: "/workspace/escape"})
	if err != nil || symlink.GetKind() != guestv1.FileKind_FILE_KIND_SYMLINK || symlink.GetSymlinkTarget() != "../secret" {
		t.Fatalf("symlink metadata = %+v, error = %v", symlink, err)
	}
	symlinkRead, err := client.ReadFile(ctx, &guestv1.ReadFileRequest{Identity: identity, Path: "/workspace/escape"})
	if err == nil {
		_, err = symlinkRead.Recv()
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("symlink read error = %v, want FailedPrecondition", err)
	}

	if _, err := client.Move(ctx, &guestv1.MoveRequest{Identity: identity, RequestId: "move-1", Source: "/workspace/project/data.bin", Destination: "/workspace/project/moved.bin"}); err != nil {
		t.Fatalf("move file: %v", err)
	}
	if _, err := client.Remove(ctx, &guestv1.RemoveRequest{Identity: identity, RequestId: "remove-1", Path: "/workspace/project", Recursive: true}); err != nil {
		t.Fatalf("remove workspace directory: %v", err)
	}
}

func TestGuestFilesRejectsStaleRuntimeIdentity(t *testing.T) {
	server := NewServer(Identity{SandboxID: "sandbox", OwnershipGeneration: 1, BootID: "boot", GuestEpoch: "epoch"})
	server.workspace = t.TempDir()
	_, err := server.Stat(t.Context(), &guestv1.StatRequest{Identity: &guestv1.RuntimeIdentity{SandboxId: "other", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}, Path: "/workspace"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale identity error = %v, want FailedPrecondition", err)
	}
}
