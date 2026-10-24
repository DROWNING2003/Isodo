package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultWorkspace = "/workspace"
	maxFileBytes     = 256 << 20
	maxChunkBytes    = 64 << 10
	maxPageSize      = 500
	maxDirectorySize = 4096
)

func (s *Server) Stat(ctx context.Context, request *guestv1.StatRequest) (*guestv1.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	relative, err := workspaceRelative(request.GetPath(), true)
	if err != nil {
		return nil, err
	}
	root, err := s.openWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, mapFileError("stat", err)
	}
	result, err := fileInfo(root, relative, info)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Server) ListDirectory(ctx context.Context, request *guestv1.ListDirectoryRequest) (*guestv1.ListDirectoryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	relative, err := workspaceRelative(request.GetPath(), true)
	if err != nil {
		return nil, err
	}
	pageSize := int(request.GetPageSize())
	if pageSize == 0 {
		pageSize = 100
	}
	if pageSize < 1 || pageSize > maxPageSize {
		return nil, status.Errorf(codes.InvalidArgument, "page_size must be between 1 and %d", maxPageSize)
	}
	root, err := s.openWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, mapFileError("list directory", err)
	}
	if !info.IsDir() {
		return nil, status.Error(codes.FailedPrecondition, "path is not a directory")
	}
	directory, err := root.Open(relative)
	if err != nil {
		return nil, mapFileError("open directory", err)
	}
	entries, err := directory.ReadDir(maxDirectorySize + 1)
	closeErr := directory.Close()
	if err != nil {
		return nil, mapFileError("read directory", err)
	}
	if closeErr != nil {
		return nil, mapFileError("close directory", closeErr)
	}
	if len(entries) > maxDirectorySize {
		return nil, status.Error(codes.ResourceExhausted, "directory exceeds the 4096-entry snapshot limit")
	}
	// ReadDir returns a stable lexical order when n <= 0; sort the bounded page
	// snapshot here because a positive n preserves filesystem order instead.
	sortDirEntries(entries)
	start := 0
	if request.GetPageToken() != "" {
		lastName, err := decodePageToken(request.GetPageToken())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid directory page token")
		}
		for start < len(entries) && entries[start].Name() <= lastName {
			start++
		}
	}
	end := min(start+pageSize, len(entries))
	response := &guestv1.ListDirectoryResponse{Entries: make([]*guestv1.FileInfo, 0, end-start)}
	for _, entry := range entries[start:end] {
		entryPath := path.Join(relative, entry.Name())
		entryInfo, err := root.Lstat(entryPath)
		if err != nil {
			return nil, mapFileError("stat directory entry", err)
		}
		item, err := fileInfo(root, entryPath, entryInfo)
		if err != nil {
			return nil, err
		}
		response.Entries = append(response.Entries, item)
	}
	if end < len(entries) && end > start {
		response.NextPageToken = encodePageToken(entries[end-1].Name())
	}
	return response, nil
}

func (s *Server) ReadFile(request *guestv1.ReadFileRequest, stream guestv1.GuestService_ReadFileServer) error {
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return err
	}
	relative, err := workspaceRelative(request.GetPath(), false)
	if err != nil {
		return err
	}
	root, err := s.openWorkspace()
	if err != nil {
		return err
	}
	defer root.Close()
	before, err := root.Lstat(relative)
	if err != nil {
		return mapFileError("stat file", err)
	}
	if !before.Mode().IsRegular() {
		return status.Error(codes.FailedPrecondition, "only regular files can be read")
	}
	if before.Size() > maxFileBytes {
		return status.Error(codes.ResourceExhausted, "file exceeds the 256 MiB limit")
	}
	file, err := root.Open(relative)
	if err != nil {
		return mapFileError("open file", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return mapFileError("stat opened file", err)
	}
	if !opened.Mode().IsRegular() || opened.Size() != before.Size() || !os.SameFile(before, opened) {
		return status.Error(codes.Aborted, "FILE_CHANGED: file changed while opening")
	}
	metadata, err := fileInfo(root, relative, before)
	if err != nil {
		return err
	}
	if err := stream.Send(&guestv1.ReadFileFrame{Frame: &guestv1.ReadFileFrame_Header{Header: &guestv1.ReadFileHeader{File: metadata}}}); err != nil {
		return err
	}
	hasher := sha256.New()
	buffer := make([]byte, maxChunkBytes)
	var offset uint64
	for {
		if err := stream.Context().Err(); err != nil {
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			chunk := buffer[:count]
			_, _ = hasher.Write(chunk)
			if err := stream.Send(&guestv1.ReadFileFrame{Frame: &guestv1.ReadFileFrame_Chunk{Chunk: &guestv1.FileChunk{Offset: offset, Data: append([]byte(nil), chunk...)}}}); err != nil {
				return err
			}
			offset += uint64(count)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return mapFileError("read file", readErr)
		}
	}
	after, err := file.Stat()
	if err != nil {
		return mapFileError("restat file", err)
	}
	if offset != uint64(before.Size()) || before.ModTime() != after.ModTime() || before.Size() != after.Size() {
		return status.Error(codes.Aborted, "FILE_CHANGED: file changed during read")
	}
	return stream.Send(&guestv1.ReadFileFrame{Frame: &guestv1.ReadFileFrame_End{End: &guestv1.ReadFileEnd{SizeBytes: offset, Sha256: hasher.Sum(nil)}}})
}

func (s *Server) WriteFile(stream guestv1.GuestService_WriteFileServer) error {
	first, err := stream.Recv()
	if err != nil {
		return mapStreamReceiveError(err)
	}
	header := first.GetHeader()
	if header == nil || first.GetChunk() != nil {
		return status.Error(codes.InvalidArgument, "the first WriteFile frame must contain a header")
	}
	if err := s.checkIdentity(header.GetIdentity()); err != nil {
		return err
	}
	if err := validateRequestID(header.GetRequestId()); err != nil {
		return err
	}
	relative, err := workspaceRelative(header.GetPath(), false)
	if err != nil {
		return err
	}
	if header.GetSizeBytes() > maxFileBytes {
		return status.Error(codes.ResourceExhausted, "file exceeds the 256 MiB limit")
	}
	if len(header.GetSha256()) != sha256.Size {
		return status.Error(codes.InvalidArgument, "sha256 must contain exactly 32 bytes")
	}
	mode, err := requestedFileMode(header.Mode)
	if err != nil {
		return err
	}
	root, err := s.openWorkspace()
	if err != nil {
		return err
	}
	defer root.Close()
	parent := path.Dir(relative)
	base := path.Base(relative)
	parentRoot, err := root.OpenRoot(parent)
	if err != nil {
		return mapFileError("open destination directory", err)
	}
	defer parentRoot.Close()
	if targetInfo, err := parentRoot.Lstat(base); err == nil {
		if !targetInfo.Mode().IsRegular() {
			return status.Error(codes.FailedPrecondition, "destination must be a regular file")
		}
		if !header.GetOverwrite() {
			return status.Error(codes.AlreadyExists, "FILE_EXISTS: destination already exists")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapFileError("inspect destination", err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return status.Error(codes.Internal, "cannot allocate upload staging file")
	}
	tempName := ".isodo-upload-" + fmt.Sprintf("%x", random[:])
	temp, err := parentRoot.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return mapFileError("create upload staging file", err)
	}
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = parentRoot.Remove(tempName)
		}
	}()
	hasher := sha256.New()
	var offset uint64
	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return mapStreamReceiveError(recvErr)
		}
		chunk := frame.GetChunk()
		if chunk == nil || frame.GetHeader() != nil {
			return status.Error(codes.InvalidArgument, "WriteFile accepts only chunk frames after the header")
		}
		if len(chunk.GetData()) == 0 || len(chunk.GetData()) > maxChunkBytes || chunk.GetOffset() != offset {
			return status.Error(codes.InvalidArgument, "invalid file chunk size or offset")
		}
		if offset+uint64(len(chunk.GetData())) > header.GetSizeBytes() {
			return status.Error(codes.InvalidArgument, "file chunks exceed declared size")
		}
		if _, err := temp.Write(chunk.GetData()); err != nil {
			return mapFileError("write upload staging file", err)
		}
		_, _ = hasher.Write(chunk.GetData())
		offset += uint64(len(chunk.GetData()))
	}
	if offset != header.GetSizeBytes() {
		return status.Error(codes.InvalidArgument, "uploaded bytes do not match declared size")
	}
	digest := hasher.Sum(nil)
	if !equalBytes(digest, header.GetSha256()) {
		return status.Error(codes.DataLoss, "HASH_MISMATCH: uploaded file hash did not match")
	}
	if err := temp.Chmod(mode); err != nil {
		return mapFileError("set upload file mode", err)
	}
	if err := temp.Sync(); err != nil {
		return mapFileError("sync upload file", err)
	}
	if err := temp.Close(); err != nil {
		return mapFileError("close upload file", err)
	}
	if header.GetOverwrite() {
		if err := parentRoot.Rename(tempName, base); err != nil {
			return mapFileError("commit uploaded file", err)
		}
	} else {
		if err := parentRoot.Link(tempName, base); err != nil {
			if errors.Is(err, os.ErrExist) {
				return status.Error(codes.AlreadyExists, "FILE_EXISTS: destination already exists")
			}
			return mapFileError("commit uploaded file", err)
		}
		if err := parentRoot.Remove(tempName); err != nil {
			return mapFileError("remove upload staging link", err)
		}
	}
	committed = true
	if dir, err := parentRoot.Open("."); err == nil {
		err = dir.Sync()
		_ = dir.Close()
		if err != nil {
			return status.Error(codes.Internal, "COMMIT_UNKNOWN: file committed but directory sync failed")
		}
	}
	info, err := parentRoot.Lstat(base)
	if err != nil {
		return mapFileError("stat committed file", err)
	}
	result, err := fileInfo(root, relative, info)
	if err != nil {
		return err
	}
	return stream.SendAndClose(&guestv1.WriteFileResponse{File: result, Sha256: digest})
}

func (s *Server) MakeDirectory(ctx context.Context, request *guestv1.MakeDirectoryRequest) (*guestv1.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.GetRequestId()); err != nil {
		return nil, err
	}
	relative, err := workspaceRelative(request.GetPath(), false)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o700)
	if request.Mode != nil {
		mode = os.FileMode(request.GetMode())
		if mode != 0o700 && mode != 0o755 {
			return nil, status.Error(codes.InvalidArgument, "directory mode must be 0700 or 0755")
		}
	}
	root, err := s.openWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if request.GetParents() {
		err = root.MkdirAll(relative, mode)
	} else {
		err = root.Mkdir(relative, mode)
	}
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, mapFileError("create directory", err)
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, mapFileError("stat directory", err)
	}
	if !info.IsDir() {
		return nil, status.Error(codes.AlreadyExists, "path exists and is not a directory")
	}
	return fileInfo(root, relative, info)
}

func (s *Server) Remove(ctx context.Context, request *guestv1.RemoveRequest) (*emptypb.Empty, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.GetRequestId()); err != nil {
		return nil, err
	}
	relative, err := workspaceRelative(request.GetPath(), false)
	if err != nil {
		return nil, err
	}
	root, err := s.openWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return new(emptypb.Empty), nil
	}
	if err != nil {
		return nil, mapFileError("inspect removal target", err)
	}
	if info.IsDir() && !request.GetRecursive() {
		directory, err := root.Open(relative)
		if err != nil {
			return nil, mapFileError("open removal directory", err)
		}
		entries, readErr := directory.ReadDir(1)
		_ = directory.Close()
		if readErr != nil {
			return nil, mapFileError("read removal directory", readErr)
		}
		if len(entries) > 0 {
			return nil, status.Error(codes.FailedPrecondition, "DIRECTORY_NOT_EMPTY: recursive removal is required")
		}
	}
	if request.GetRecursive() && info.IsDir() {
		count, err := countTree(root, relative, maxDirectorySize)
		if err != nil {
			return nil, err
		}
		if count > maxDirectorySize {
			return nil, status.Error(codes.ResourceExhausted, "REMOVE_LIMIT: recursive removal exceeds 4096 entries")
		}
		err = root.RemoveAll(relative)
	} else {
		err = root.Remove(relative)
	}
	if err != nil {
		return nil, mapFileError("remove path", err)
	}
	return new(emptypb.Empty), nil
}

func (s *Server) Move(ctx context.Context, request *guestv1.MoveRequest) (*guestv1.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkIdentity(request.GetIdentity()); err != nil {
		return nil, err
	}
	if err := validateRequestID(request.GetRequestId()); err != nil {
		return nil, err
	}
	source, err := workspaceRelative(request.GetSource(), false)
	if err != nil {
		return nil, err
	}
	destination, err := workspaceRelative(request.GetDestination(), false)
	if err != nil {
		return nil, err
	}
	if source == destination {
		return s.statRelative(destination)
	}
	root, err := s.openWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	sourceInfo, err := root.Lstat(source)
	if err != nil {
		return nil, mapFileError("inspect move source", err)
	}
	if info, err := root.Lstat(destination); err == nil {
		if !request.GetOverwrite() {
			return nil, status.Error(codes.AlreadyExists, "FILE_EXISTS: destination already exists")
		}
		if !info.Mode().IsRegular() || !sourceInfo.Mode().IsRegular() {
			return nil, status.Error(codes.FailedPrecondition, "overwrite move only replaces regular files")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, mapFileError("inspect move destination", err)
	}
	if err := root.Rename(source, destination); err != nil {
		return nil, mapFileError("move path", err)
	}
	info, err := root.Lstat(destination)
	if err != nil {
		return nil, mapFileError("stat moved path", err)
	}
	return fileInfo(root, destination, info)
}

func (s *Server) statRelative(relative string) (*guestv1.FileInfo, error) {
	root, err := s.openWorkspace()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(relative)
	if err != nil {
		return nil, mapFileError("stat path", err)
	}
	return fileInfo(root, relative, info)
}

func (s *Server) openWorkspace() (*os.Root, error) {
	workspace := s.workspace
	if workspace == "" {
		workspace = defaultWorkspace
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "workspace is not ready: %v", err)
	}
	return root, nil
}

func workspaceRelative(value string, allowRoot bool) (string, error) {
	if value == "" || len(value) > 4096 || strings.ContainsRune(value, 0) || !path.IsAbs(value) {
		return "", status.Error(codes.InvalidArgument, "INVALID_PATH: path must be an absolute workspace path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", status.Error(codes.InvalidArgument, "INVALID_PATH: parent traversal is not allowed")
		}
	}
	if value != defaultWorkspace && !strings.HasPrefix(value, defaultWorkspace+"/") {
		return "", status.Error(codes.InvalidArgument, "INVALID_PATH: path must be beneath /workspace")
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(value, defaultWorkspace), "/")
	if relative == "" {
		if !allowRoot {
			return "", status.Error(codes.InvalidArgument, "INVALID_PATH: workspace root cannot be modified")
		}
		return ".", nil
	}
	clean := path.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", status.Error(codes.InvalidArgument, "INVALID_PATH: path escapes the workspace")
	}
	return clean, nil
}

func fileInfo(root *os.Root, relative string, info os.FileInfo) (*guestv1.FileInfo, error) {
	kind := guestv1.FileKind_FILE_KIND_UNSPECIFIED
	switch {
	case info.Mode().IsRegular():
		kind = guestv1.FileKind_FILE_KIND_REGULAR
	case info.IsDir():
		kind = guestv1.FileKind_FILE_KIND_DIRECTORY
	case info.Mode()&os.ModeSymlink != 0:
		kind = guestv1.FileKind_FILE_KIND_SYMLINK
	default:
		return nil, status.Error(codes.FailedPrecondition, "special files are not exposed by the Files API")
	}
	logical := defaultWorkspace
	if relative != "." {
		logical = path.Join(defaultWorkspace, relative)
	}
	result := &guestv1.FileInfo{
		Path:       logical,
		Name:       path.Base(logical),
		Kind:       kind,
		SizeBytes:  uint64(max(info.Size(), 0)),
		Mode:       uint32(info.Mode().Perm()),
		ModifiedAt: timestamppb.New(info.ModTime()),
	}
	if kind == guestv1.FileKind_FILE_KIND_SYMLINK {
		target, err := root.Readlink(relative)
		if err != nil {
			return nil, mapFileError("read symlink metadata", err)
		}
		result.SymlinkTarget = &target
	}
	return result, nil
}

func mapFileError(action string, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return status.Errorf(codes.NotFound, "FILE_NOT_FOUND: %s", action)
	case errors.Is(err, os.ErrPermission):
		return status.Errorf(codes.PermissionDenied, "ACCESS_DENIED: %s", action)
	default:
		return status.Errorf(codes.Internal, "%s: %v", action, err)
	}
}

func mapStreamReceiveError(err error) error {
	if errors.Is(err, io.EOF) {
		return status.Error(codes.InvalidArgument, "WriteFile requires a header frame")
	}
	return err
}

func validateRequestID(value string) error {
	if len(value) < 1 || len(value) > 128 {
		return status.Error(codes.InvalidArgument, "request_id must contain 1 to 128 ASCII safe characters")
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' && char != ':' {
			return status.Error(codes.InvalidArgument, "request_id must contain only ASCII safe characters")
		}
	}
	return nil
}

func requestedFileMode(value *uint32) (os.FileMode, error) {
	if value == nil {
		return 0o600, nil
	}
	mode := os.FileMode(*value)
	if mode != 0o600 && mode != 0o644 && mode != 0o700 && mode != 0o755 {
		return 0, status.Error(codes.InvalidArgument, "file mode must be 0600, 0644, 0700, or 0755")
	}
	return mode, nil
}

func encodePageToken(name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name))
}

func decodePageToken(token string) (string, error) {
	name, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(name) == 0 || strings.ContainsAny(string(name), "/\x00") {
		return "", errors.New("invalid page token")
	}
	return string(name), nil
}

func sortDirEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
}

func countTree(root *os.Root, relative string, limit int) (int, error) {
	count := 0
	var visit func(string) error
	visit = func(current string) error {
		if count > limit {
			return nil
		}
		file, err := root.Open(current)
		if err != nil {
			return mapFileError("open recursive removal directory", err)
		}
		entries, err := file.ReadDir(limit - count + 1)
		_ = file.Close()
		if err != nil {
			return mapFileError("read recursive removal directory", err)
		}
		for _, entry := range entries {
			count++
			if entry.IsDir() {
				if err := visit(path.Join(current, entry.Name())); err != nil {
					return err
				}
			}
			if count > limit {
				return nil
			}
		}
		return nil
	}
	if err := visit(relative); err != nil {
		return 0, err
	}
	return count, nil
}

func equalBytes(first, second []byte) bool {
	if len(first) != len(second) {
		return false
	}
	var difference byte
	for index := range first {
		difference |= first[index] ^ second[index]
	}
	return difference == 0
}
