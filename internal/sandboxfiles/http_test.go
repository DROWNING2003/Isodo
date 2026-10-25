package sandboxfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type memoryBackend struct {
	data                       []byte
	exists, corrupt, overwrite bool
	writes                     int
}

func (b *memoryBackend) Write(_ context.Context, h *nodev1.RuntimeWriteHeader, r io.Reader) (*guestv1.WriteFileResponse, error) {
	b.writes++
	b.overwrite = h.Overwrite
	if b.exists && !h.Overwrite {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if !bytes.Equal(digest[:], h.Sha256) || uint64(len(data)) != h.SizeBytes {
		return nil, status.Error(codes.DataLoss, "bad upload")
	}
	b.data = bytes.Clone(data)
	b.exists = true
	return &guestv1.WriteFileResponse{File: &guestv1.FileInfo{Path: h.Path, SizeBytes: h.SizeBytes}, Sha256: digest[:]}, nil
}
func (b *memoryBackend) Read(_ context.Context, r *nodev1.RuntimeReadRequest, send func(*guestv1.ReadFileFrame) error) error {
	if err := send(&guestv1.ReadFileFrame{Frame: &guestv1.ReadFileFrame_Header{Header: &guestv1.ReadFileHeader{File: &guestv1.FileInfo{Path: r.Path, Kind: guestv1.FileKind_FILE_KIND_REGULAR, SizeBytes: uint64(len(b.data))}}}}); err != nil {
		return err
	}
	for offset := 0; offset < len(b.data); offset += ChunkBytes {
		end := min(offset+ChunkBytes, len(b.data))
		if err := send(&guestv1.ReadFileFrame{Frame: &guestv1.ReadFileFrame_Chunk{Chunk: &guestv1.FileChunk{Offset: uint64(offset), Data: b.data[offset:end]}}}); err != nil {
			return err
		}
	}
	digest := sha256.Sum256(b.data)
	if b.corrupt {
		digest[0] ^= 255
	}
	return send(&guestv1.ReadFileFrame{Frame: &guestv1.ReadFileFrame_End{End: &guestv1.ReadFileEnd{SizeBytes: uint64(len(b.data)), Sha256: digest[:]}}})
}
func (*memoryBackend) Mkdir(context.Context, *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error) {
	return &guestv1.FileInfo{}, nil
}
func testHTTP(b Backend) *HTTP {
	return NewHTTP(b, func(http.ResponseWriter, *http.Request, string) (*nodev1.SandboxTarget, bool) {
		return &nodev1.SandboxTarget{SandboxId: "sandbox", OwnershipGeneration: 2, Revision: 7}, true
	})
}
func uploadRequest(t *testing.T, data []byte, suffix string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "upload.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/files?path=/workspace/config.bin"+suffix, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
func TestMultipartUploadDefaultOverwriteAndVerifiedBinaryDownload(t *testing.T) {
	backend := &memoryBackend{data: []byte("old"), exists: true}
	handler := testHTTP(backend)
	data := bytes.Repeat([]byte{0, 255, 128, 1, 2, 3, 10}, 40000)
	response := httptest.NewRecorder()
	handler.Upload(response, uploadRequest(t, data, ""), "sandbox")
	if response.Code != 200 || !backend.overwrite || !bytes.Equal(backend.data, data) {
		t.Fatalf("upload status=%d overwrite=%v", response.Code, backend.overwrite)
	}
	digest := sha256.Sum256(data)
	response = httptest.NewRecorder()
	handler.Download(response, httptest.NewRequest(http.MethodGet, "/files?path=/workspace/config.bin", nil), "sandbox")
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) || response.Header().Get("X-Content-SHA256") != hex.EncodeToString(digest[:]) {
		t.Fatal("binary download was not verified")
	}
	response = httptest.NewRecorder()
	handler.Upload(response, uploadRequest(t, []byte("new"), "&overwrite=false"), "sandbox")
	if response.Code != 409 || !bytes.Equal(backend.data, data) {
		t.Fatal("overwrite=false modified the existing file")
	}
}
func TestCorruptDownloadReturnsNoFileBytes(t *testing.T) {
	backend := &memoryBackend{data: []byte("private-file-content"), corrupt: true}
	response := httptest.NewRecorder()
	testHTTP(backend).Download(response, httptest.NewRequest(http.MethodGet, "/files?path=/workspace/config.bin", nil), "sandbox")
	if response.Code != 502 || strings.Contains(response.Body.String(), "private-file-content") || !strings.Contains(response.Body.String(), "FILE_INTEGRITY_FAILED") {
		t.Fatal("unverified file content reached HTTP response")
	}
}
func TestUploadRejectsTraversalAndReservedPathsBeforeBackend(t *testing.T) {
	for _, destination := range []string{"/etc/passwd", "/workspace/../etc/passwd", "/workspace/.isodo/token", "/workspace/.t3/session.db"} {
		backend := &memoryBackend{}
		request := uploadRequest(t, []byte("x"), "")
		query := request.URL.Query()
		query.Set("path", destination)
		request.URL.RawQuery = query.Encode()
		response := httptest.NewRecorder()
		testHTTP(backend).Upload(response, request, "sandbox")
		if response.Code < 400 || backend.writes != 0 {
			t.Fatalf("allowed %s", destination)
		}
	}
}
