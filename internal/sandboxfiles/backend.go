// Package sandboxfiles defines the bounded file transfer shared by Control and
// local Node. Guest remains responsible for filesystem safety and atomic commit.
package sandboxfiles

import (
	"context"
	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"path"
	"strings"
)

const MaxBytes = 256 << 20
const ChunkBytes = 64 << 10

type Backend interface {
	Write(context.Context, *nodev1.RuntimeWriteHeader, io.Reader) (*guestv1.WriteFileResponse, error)
	Read(context.Context, *nodev1.RuntimeReadRequest, func(*guestv1.ReadFileFrame) error) error
	Mkdir(context.Context, *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error)
}

func ValidatePath(value string, write bool) error {
	if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") || path.Clean(value) != value || (value != "/workspace" && !strings.HasPrefix(value, "/workspace/")) {
		return status.Error(codes.InvalidArgument, "a canonical absolute workspace path is required")
	}
	if write && (value == "/workspace" || value == "/workspace/.isodo" || value == "/workspace/.t3" || strings.HasPrefix(value, "/workspace/.isodo/") || strings.HasPrefix(value, "/workspace/.t3/")) {
		return status.Error(codes.PermissionDenied, "runtime-owned paths are not writable through Files")
	}
	return nil
}
