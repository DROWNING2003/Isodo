package sandboxfiles

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FileMetadata is the public representation; Guest enum/wire fields stay private.
type FileMetadata struct {
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	SizeBytes     uint64    `json:"size_bytes"`
	Mode          uint32    `json:"mode"`
	ModifiedAt    time.Time `json:"modified_at"`
	SymlinkTarget *string   `json:"symlink_target,omitempty"`
}

func metadata(info *guestv1.FileInfo) (FileMetadata, error) {
	if info == nil || ValidatePath(info.GetPath(), false) != nil || info.Name != path.Base(info.Path) || !info.ModifiedAt.IsValid() {
		return FileMetadata{}, status.Error(codes.DataLoss, "invalid file metadata")
	}
	kinds := map[guestv1.FileKind]string{guestv1.FileKind_FILE_KIND_REGULAR: "regular", guestv1.FileKind_FILE_KIND_DIRECTORY: "directory", guestv1.FileKind_FILE_KIND_SYMLINK: "symlink"}
	kind, ok := kinds[info.Kind]
	if !ok {
		return FileMetadata{}, status.Error(codes.DataLoss, "invalid file kind")
	}
	return FileMetadata{Path: info.Path, Name: info.Name, Kind: kind, SizeBytes: info.SizeBytes, Mode: info.Mode, ModifiedAt: info.ModifiedAt.AsTime(), SymlinkTarget: info.SymlinkTarget}, nil
}
func metadataJSON(w http.ResponseWriter, info *guestv1.FileInfo, expected string) {
	result, err := metadata(info)
	if err == nil && result.Path != expected {
		err = status.Error(codes.DataLoss, "unexpected metadata path")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeMetadataJSON(w, result)
}
func writeMetadataJSON(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
func (h *HTTP) Stat(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	p := r.URL.Query().Get("path")
	if err := ValidatePath(p, false); err != nil {
		writeError(w, err)
		return
	}
	info, err := h.backend.Stat(ctx, &nodev1.RuntimeReadRequest{Target: target, Path: p})
	if err != nil {
		writeError(w, err)
		return
	}
	metadataJSON(w, info, p)
}
func (h *HTTP) ListDirectory(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	p := r.URL.Query().Get("path")
	if err := ValidatePath(p, false); err != nil {
		writeError(w, err)
		return
	}
	size := uint64(100)
	if raw, exists := r.URL.Query()["page_size"]; exists {
		var err error
		size, err = strconv.ParseUint(raw[0], 10, 32)
		if err != nil || size < 1 || size > 500 {
			fail(w, 400, "INVALID_PAGE_SIZE", "page_size must be between 1 and 500.", false)
			return
		}
	}
	token := r.URL.Query().Get("page_token")
	if len(token) > 1024 {
		fail(w, 400, "INVALID_PAGE_TOKEN", "page_token is too long.", false)
		return
	}
	result, err := h.backend.List(ctx, &nodev1.RuntimeListRequest{Target: target, Path: p, PageSize: uint32(size), PageToken: token})
	if err != nil {
		writeError(w, err)
		return
	}
	if result == nil || len(result.Entries) > int(size) {
		writeError(w, status.Error(codes.DataLoss, "invalid directory page"))
		return
	}
	entries := make([]FileMetadata, 0, len(result.Entries))
	for _, info := range result.Entries {
		item, err := metadata(info)
		if err != nil || path.Dir(item.Path) != p || item.Path == p {
			writeError(w, status.Error(codes.DataLoss, "invalid directory entry"))
			return
		}
		entries = append(entries, item)
	}
	writeMetadataJSON(w, struct {
		Entries       []FileMetadata `json:"entries"`
		NextPageToken string         `json:"next_page_token"`
	}{entries, result.NextPageToken})
}
func (h *HTTP) Remove(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	p := r.URL.Query().Get("path")
	if err := ValidatePath(p, true); err != nil {
		writeError(w, err)
		return
	}
	recursive := false
	if raw, exists := r.URL.Query()["recursive"]; exists {
		var err error
		recursive, err = strconv.ParseBool(raw[0])
		if err != nil {
			fail(w, 400, "INVALID_RECURSIVE", "recursive must be true or false.", false)
			return
		}
	}
	if _, err := h.backend.Remove(ctx, &nodev1.RuntimeRemoveRequest{Target: target, Path: p, Recursive: recursive, RequestId: "remove-" + uuid.NewString()}); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *HTTP) Move(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	var input struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
		Overwrite   *bool  `json:"overwrite"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fail(w, 400, "INVALID_JSON", "Supply source and destination paths.", false)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		fail(w, 400, "INVALID_JSON", "Supply one JSON object.", false)
		return
	}
	for _, p := range []string{input.Source, input.Destination} {
		if err := ValidatePath(p, true); err != nil {
			writeError(w, err)
			return
		}
	}
	overwrite := true
	if input.Overwrite != nil {
		overwrite = *input.Overwrite
	}
	info, err := h.backend.Move(ctx, &nodev1.RuntimeMoveRequest{Target: target, Source: input.Source, Destination: input.Destination, Overwrite: overwrite, RequestId: "move-" + uuid.NewString()})
	if err != nil {
		writeError(w, err)
		return
	}
	metadataJSON(w, info, input.Destination)
}
