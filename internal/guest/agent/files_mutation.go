package agent

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Mutations cannot follow parent symlinks: an alias inside the workspace must
// not make runtime-owned directories writable. Each directory is pinned by FD
// before resolving the next segment, avoiding check-then-open path races.
func (s *Server) mutationParent(logical string) (*os.Root, string, error) {
	relative, err := workspaceRelative(logical, false)
	if err != nil {
		return nil, "", err
	}
	if relative == ".isodo" || relative == ".t3" || strings.HasPrefix(relative, ".isodo/") || strings.HasPrefix(relative, ".t3/") {
		return nil, "", status.Error(codes.PermissionDenied, "runtime-owned paths cannot be mutated")
	}
	root, err := s.openWorkspace()
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, "", mapFileError("open workspace directory", err)
	}
	defer func() { _ = directory.Close() }()
	for segment := range strings.SplitSeq(path.Dir(relative), "/") {
		if segment == "." {
			continue
		}
		fd, err := unix.Openat(int(directory.Fd()), segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
				return nil, "", status.Error(codes.PermissionDenied, "symlink parents are forbidden for mutations")
			}
			return nil, "", mapFileError("open mutation parent", err)
		}
		_ = directory.Close()
		directory = os.NewFile(uintptr(fd), segment)
	}
	prefix := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd"
	}
	parent, err := os.OpenRoot(fmt.Sprintf("%s/%d", prefix, directory.Fd()))
	if err != nil {
		return nil, "", mapFileError("pin mutation parent", err)
	}
	return parent, path.Base(relative), nil
}
