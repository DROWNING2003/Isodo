package sandboxfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Resolver func(http.ResponseWriter, *http.Request, string) (*nodev1.SandboxTarget, bool)
type HTTP struct {
	backend Backend
	resolve Resolver
	slots   chan struct{}
}

func NewHTTP(backend Backend, resolve Resolver) *HTTP {
	return &HTTP{backend: backend, resolve: resolve, slots: make(chan struct{}, 4)}
}

type WriteResult struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (h *HTTP) begin(w http.ResponseWriter, r *http.Request, id string) (*nodev1.SandboxTarget, context.Context, func(), bool) {
	target, ok := h.resolve(w, r, id)
	if !ok {
		return nil, nil, nil, false
	}
	select {
	case h.slots <- struct{}{}:
	default:
		fail(w, 429, "FILE_TRANSFER_LIMIT", "Too many active file transfers.", true)
		return nil, nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	stopBody := context.AfterFunc(ctx, func() {
		if r.Body != nil {
			_ = r.Body.Close()
		}
	})
	return target, ctx, func() {
		stopBody()
		cancel()
		<-h.slots
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	}, true
}
func (h *HTTP) Upload(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	destination := r.URL.Query().Get("path")
	if err := ValidatePath(destination, true); err != nil {
		writeError(w, err)
		return
	}
	overwrite := true
	if raw := r.URL.Query().Get("overwrite"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			fail(w, 400, "INVALID_OVERWRITE", "overwrite must be true or false.", false)
			return
		}
		overwrite = value
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBytes+(1<<20))
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	multipart, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "INVALID_MULTIPART", "Supply one multipart file part.", false)
		return
	}
	part, err := multipart.NextPart()
	if err != nil || part.FormName() != "file" {
		fail(w, 400, "INVALID_MULTIPART", "The first and only part must be named file.", false)
		return
	}
	defer part.Close()
	temporary, err := os.CreateTemp("", "isodo-upload-*")
	if err != nil {
		fail(w, 503, "FILE_STAGING_UNAVAILABLE", "File staging is unavailable.", true)
		return
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	hash := sha256.New()
	var size int64
	buffer := make([]byte, ChunkBytes)
	for {
		if err := ctx.Err(); err != nil {
			writeError(w, status.Error(codes.DeadlineExceeded, "upload timed out"))
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
		n, readErr := part.Read(buffer)
		if n > 0 {
			size += int64(n)
			if size > MaxBytes {
				fail(w, 413, "FILE_TOO_LARGE", "Files are limited to 256 MiB.", false)
				return
			}
			if _, err := temporary.Write(buffer[:n]); err != nil {
				fail(w, 503, "FILE_STAGING_UNAVAILABLE", "File staging is unavailable.", true)
				return
			}
			_, _ = hash.Write(buffer[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			fail(w, 400, "FILE_BODY_INCOMPLETE", "File upload was interrupted.", false)
			return
		}
	}
	if _, err := multipart.NextPart(); !errors.Is(err, io.EOF) {
		fail(w, 400, "INVALID_MULTIPART", "Exactly one file part is supported.", false)
		return
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		writeError(w, err)
		return
	}
	digest := hash.Sum(nil)
	result, err := h.backend.Write(ctx, &nodev1.RuntimeWriteHeader{Target: target, Path: destination, SizeBytes: uint64(size), Sha256: digest, Overwrite: overwrite, RequestId: "file-" + uuid.NewString()}, temporary)
	if err != nil {
		writeError(w, err)
		return
	}
	if result == nil || result.GetFile().GetPath() != destination || result.GetFile().GetSizeBytes() != uint64(size) || !bytes.Equal(result.Sha256, digest) {
		fail(w, 503, "COMMIT_UNKNOWN", "Verify the target file before retrying the upload.", false)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(WriteResult{Path: destination, Bytes: size, SHA256: hex.EncodeToString(digest)})
}
func (h *HTTP) Download(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	source := r.URL.Query().Get("path")
	if err := ValidatePath(source, false); err != nil {
		writeError(w, err)
		return
	}
	temporary, err := os.CreateTemp("", "isodo-download-*")
	if err != nil {
		fail(w, 503, "FILE_STAGING_UNAVAILABLE", "File staging is unavailable.", true)
		return
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	hash := sha256.New()
	var size, expected uint64
	headerSeen, endSeen := false, false
	var digest []byte
	err = h.backend.Read(ctx, &nodev1.RuntimeReadRequest{Target: target, Path: source}, func(frame *guestv1.ReadFileFrame) error {
		invalid := func() error { return status.Error(codes.DataLoss, "invalid file stream") }
		if endSeen {
			return invalid()
		}
		switch {
		case frame.GetHeader() != nil:
			file := frame.GetHeader().GetFile()
			if headerSeen || file.GetPath() != source || file.GetKind() != guestv1.FileKind_FILE_KIND_REGULAR || file.GetSizeBytes() > MaxBytes {
				return invalid()
			}
			headerSeen = true
			expected = file.GetSizeBytes()
		case frame.GetChunk() != nil:
			chunk := frame.GetChunk()
			if !headerSeen || chunk.Offset != size || len(chunk.Data) == 0 || len(chunk.Data) > ChunkBytes || size+uint64(len(chunk.Data)) > expected {
				return invalid()
			}
			if _, err := temporary.Write(chunk.Data); err != nil {
				return err
			}
			_, _ = hash.Write(chunk.Data)
			size += uint64(len(chunk.Data))
		case frame.GetEnd() != nil:
			end := frame.GetEnd()
			if !headerSeen || size != expected || end.SizeBytes != size || len(end.Sha256) != sha256.Size || !bytes.Equal(end.Sha256, hash.Sum(nil)) {
				return invalid()
			}
			endSeen = true
			digest = bytes.Clone(end.Sha256)
		default:
			return invalid()
		}
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	if !endSeen {
		writeError(w, status.Error(codes.DataLoss, "file stream has no verified completion"))
		return
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatUint(size, 10))
	w.Header().Set("X-Content-SHA256", hex.EncodeToString(digest))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(source)}))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.CopyBuffer(deadlineWriter{w: w}, temporary, make([]byte, ChunkBytes))
}

type deadlineWriter struct{ w http.ResponseWriter }

func (d deadlineWriter) Write(data []byte) (int, error) {
	_ = http.NewResponseController(d.w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	return d.w.Write(data)
}
func (h *HTTP) MakeDirectory(w http.ResponseWriter, r *http.Request, id string) {
	target, ctx, done, ok := h.begin(w, r, id)
	if !ok {
		return
	}
	defer done()
	var input struct {
		Path    string `json:"path"`
		Parents *bool  `json:"parents"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fail(w, 400, "INVALID_JSON", "Supply a directory path.", false)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		fail(w, 400, "INVALID_JSON", "Supply one JSON object.", false)
		return
	}
	if err := ValidatePath(input.Path, true); err != nil {
		writeError(w, err)
		return
	}
	parents := true
	if input.Parents != nil {
		parents = *input.Parents
	}
	_, err := h.backend.Mkdir(ctx, &nodev1.RuntimeDirectoryRequest{Target: target, Path: input.Path, Parents: parents, RequestId: "mkdir-" + uuid.NewString()})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": input.Path})
}
func writeError(w http.ResponseWriter, err error) {
	httpStatus, code, retryable := 502, "FILE_TRANSFER_FAILED", false
	switch status.Code(err) {
	case codes.InvalidArgument:
		httpStatus, code = 400, "INVALID_FILE_REQUEST"
	case codes.PermissionDenied:
		httpStatus, code = 403, "FILE_FORBIDDEN"
	case codes.NotFound:
		httpStatus, code = 404, "FILE_NOT_FOUND"
	case codes.AlreadyExists:
		httpStatus, code = 409, "FILE_EXISTS"
	case codes.FailedPrecondition, codes.Aborted:
		httpStatus, code = 409, "SANDBOX_STATE_CONFLICT"
	case codes.DataLoss:
		code = "FILE_INTEGRITY_FAILED"
	case codes.ResourceExhausted:
		httpStatus, code = 413, "FILE_TOO_LARGE"
	case codes.Unavailable:
		httpStatus, code, retryable = 503, "FILE_BACKEND_UNAVAILABLE", true
	case codes.DeadlineExceeded:
		httpStatus, code = 504, "FILE_TIMEOUT"
	case codes.Unknown:
		if strings.Contains(status.Convert(err).Message(), "COMMIT_UNKNOWN") {
			httpStatus, code = 503, "COMMIT_UNKNOWN"
		}
	}
	fail(w, httpStatus, code, "File operation could not be completed. "+code, retryable)
}
func fail(w http.ResponseWriter, httpStatus int, code, message string, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable}})
}
