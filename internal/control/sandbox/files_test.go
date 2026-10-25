package sandbox_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fileBackendFake struct {
	writes int
	target *nodev1.SandboxTarget
	data   []byte
}

func (f *fileBackendFake) Write(_ context.Context, h *nodev1.RuntimeWriteHeader, r io.Reader) (*guestv1.WriteFileResponse, error) {
	f.writes++
	f.target = h.Target
	f.data, _ = io.ReadAll(r)
	sum := sha256.Sum256(f.data)
	return &guestv1.WriteFileResponse{File: &guestv1.FileInfo{Path: h.Path, SizeBytes: uint64(len(f.data))}, Sha256: sum[:]}, nil
}
func (*fileBackendFake) Read(context.Context, *nodev1.RuntimeReadRequest, func(*guestv1.ReadFileFrame) error) error {
	return nil
}
func (*fileBackendFake) Mkdir(context.Context, *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error) {
	return &guestv1.FileInfo{}, nil
}

type fileKeyAuth struct{}

func (fileKeyAuth) Authenticate(_ context.Context, header string) (auth.Identity, error) {
	tenant := "tenant-a"
	if header == "Bearer foreign" {
		tenant = "tenant-b"
	}
	return auth.Identity{TenantID: tenant, Credential: "api_key", Scopes: []string{"sandboxes:read", "sandboxes:write"}}, nil
}
func TestFileUploadAllowsScopedOwnerAndRejectsForeignSandbox(t *testing.T) {
	store := newMemoryStore()
	item := store.items[otherSandboxID]
	item.NodeID = "node-a"
	item.OwnershipGeneration = 2
	item.Revision = "7"
	store.items[item.ID] = item
	backend := &fileBackendFake{}
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(&runtimeFake{})
	handler.SetFiles(backend)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: fileKeyAuth{}})
	makeRequest := func(authorization string) *http.Request {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "ignored-name")
		_, _ = io.WriteString(part, "model = \"test-model\"\n")
		_ = writer.Close()
		request := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+item.ID+"/files?path=/workspace/.codex/config.toml", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("Authorization", authorization)
		return request
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, makeRequest("Bearer owner"))
	if response.Code != 200 || backend.writes != 1 || backend.target.GetRevision() != 7 || backend.target.GetOwnershipGeneration() != 2 || !strings.Contains(string(backend.data), "test-model") {
		t.Fatalf("owner upload status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, makeRequest("Bearer foreign"))
	if response.Code != 404 || backend.writes != 1 {
		t.Fatal("foreign sandbox upload reached Node")
	}
}
