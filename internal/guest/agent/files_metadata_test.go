package agent

import (
	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataMutationsProtectRuntimeAndEmptyDirectory(t *testing.T) {
	s := NewServer(Identity{SandboxID: "sandbox", OwnershipGeneration: 1, BootID: "boot", GuestEpoch: "epoch"})
	s.workspace = t.TempDir()
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	for _, dir := range []string{"empty", ".isodo", ".t3"} {
		if err := os.Mkdir(filepath.Join(s.workspace, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Remove(t.Context(), &guestv1.RemoveRequest{Identity: identity, RequestId: "remove-empty", Path: "/workspace/empty"}); err != nil {
		t.Errorf("empty directory removal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.workspace, ".isodo", "protected"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".isodo", filepath.Join(s.workspace, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/workspace/.isodo/protected", "/workspace/alias/protected", "/workspace/.t3"} {
		if _, err := s.Remove(t.Context(), &guestv1.RemoveRequest{Identity: identity, RequestId: "remove-denied", Path: p, Recursive: true}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("remove %s: %v", p, err)
		}
	}
	if _, err := s.Move(t.Context(), &guestv1.MoveRequest{Identity: identity, RequestId: "move-denied", Source: "/workspace/.isodo", Destination: "/workspace/moved"}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("move runtime: %v", err)
	}
}

func TestMoveNoReplaceIsAtomic(t *testing.T) {
	s := NewServer(Identity{SandboxID: "sandbox", OwnershipGeneration: 1, BootID: "boot", GuestEpoch: "epoch"})
	s.workspace = t.TempDir()
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	for _, p := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(s.workspace, p), []byte(p), 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, p := range []string{"one", "two"} {
		go func() {
			<-start
			_, err := s.Move(t.Context(), &guestv1.MoveRequest{Identity: identity, RequestId: "move-" + p, Source: "/workspace/" + p, Destination: "/workspace/winner", Overwrite: false})
			results <- err
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if status.Code(err) == codes.AlreadyExists {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
}
func TestRemoveMissingAndMoveOverwriteSemantics(t *testing.T) {
	s := NewServer(Identity{SandboxID: "sandbox", OwnershipGeneration: 1, BootID: "boot", GuestEpoch: "epoch"})
	s.workspace = t.TempDir()
	identity := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	for _, p := range []string{"missing", "missing-parent/file"} {
		if _, err := s.Remove(t.Context(), &guestv1.RemoveRequest{Identity: identity, RequestId: "remove", Path: "/workspace/" + p}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"source", "destination"} {
		_ = os.WriteFile(filepath.Join(s.workspace, p), []byte(p), 0600)
	}
	if _, err := s.Move(t.Context(), &guestv1.MoveRequest{Identity: identity, RequestId: "move", Source: "/workspace/source", Destination: "/workspace/destination", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(s.workspace, "destination"))
	if err != nil || string(actual) != "source" {
		t.Fatal("overwrite failed")
	}
	_ = os.Mkdir(filepath.Join(s.workspace, "directory"), 0700)
	if _, err := s.Move(t.Context(), &guestv1.MoveRequest{Identity: identity, RequestId: "move-directory", Source: "/workspace/destination", Destination: "/workspace/directory", Overwrite: true}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal(err)
	}
}

func TestMetadataRejectsEveryStaleIdentity(t *testing.T) {
	s := NewServer(Identity{SandboxID: "sandbox", OwnershipGeneration: 1, BootID: "boot", GuestEpoch: "epoch"})
	s.workspace = t.TempDir()
	base := &guestv1.RuntimeIdentity{SandboxId: "sandbox", OwnershipGeneration: 1, BootId: "boot", GuestEpoch: "epoch"}
	for _, field := range []string{"sandbox", "generation", "boot", "epoch"} {
		t.Run(field, func(t *testing.T) {
			identity := proto.Clone(base).(*guestv1.RuntimeIdentity)
			switch field {
			case "sandbox":
				identity.SandboxId = "other"
			case "generation":
				identity.OwnershipGeneration++
			case "boot":
				identity.BootId = "old"
			case "epoch":
				identity.GuestEpoch = "old"
			}
			_, statErr := s.Stat(t.Context(), &guestv1.StatRequest{Identity: identity, Path: "/workspace/file"})
			_, listErr := s.ListDirectory(t.Context(), &guestv1.ListDirectoryRequest{Identity: identity, Path: "/workspace"})
			_, removeErr := s.Remove(t.Context(), &guestv1.RemoveRequest{Identity: identity, RequestId: "remove", Path: "/workspace/file"})
			_, moveErr := s.Move(t.Context(), &guestv1.MoveRequest{Identity: identity, RequestId: "move", Source: "/workspace/file", Destination: "/workspace/new"})
			for name, err := range map[string]error{"Stat": statErr, "ListDirectory": listErr, "Remove": removeErr, "Move": moveErr} {
				if status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("%s accepted stale %s: %v", name, field, err)
				}
			}
		})
	}
}
