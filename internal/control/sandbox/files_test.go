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
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
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
	item.DesiredState, item.ObservedState = "running", "running"
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

func (f *fileBackendFake) Stat(_ context.Context, r *nodev1.RuntimeReadRequest) (*guestv1.FileInfo, error) {
	f.writes++
	f.target = r.Target
	return &guestv1.FileInfo{Path: r.Path, Name: path.Base(r.Path), Kind: guestv1.FileKind_FILE_KIND_REGULAR, ModifiedAt: timestamppb.Now()}, nil
}
func (f *fileBackendFake) List(_ context.Context, r *nodev1.RuntimeListRequest) (*guestv1.ListDirectoryResponse, error) {
	f.writes++
	f.target = r.Target
	return &guestv1.ListDirectoryResponse{}, nil
}
func (f *fileBackendFake) Remove(_ context.Context, r *nodev1.RuntimeRemoveRequest) (*emptypb.Empty, error) {
	f.writes++
	f.target = r.Target
	return &emptypb.Empty{}, nil
}
func (f *fileBackendFake) Move(_ context.Context, r *nodev1.RuntimeMoveRequest) (*guestv1.FileInfo, error) {
	f.writes++
	f.target = r.Target
	return &guestv1.FileInfo{Path: r.Destination, Name: path.Base(r.Destination), Kind: guestv1.FileKind_FILE_KIND_REGULAR, ModifiedAt: timestamppb.Now()}, nil
}
func TestMetadataRoutesRequireOwnershipAndCarryTarget(t *testing.T) {
	store := newMemoryStore()
	item := store.items[otherSandboxID]
	item.DesiredState, item.ObservedState = "running", "running"
	item.NodeID = "node-a"
	item.OwnershipGeneration = 2
	item.Revision = "7"
	store.items[item.ID] = item
	backend := &fileBackendFake{}
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(&runtimeFake{})
	handler.SetFiles(backend)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: fileKeyAuth{}})
	for _, tc := range []struct {
		method, suffix, body string
		want                 int
	}{{"GET", "/files/stat?path=/workspace/file", "", 200}, {"GET", "/directories?path=/workspace", "", 200}, {"DELETE", "/files?path=/workspace/file", "", 204}, {"POST", "/files/move", `{"source":"/workspace/a","destination":"/workspace/b"}`, 200}} {
		for _, owner := range []bool{false, true} {
			before := backend.writes
			request := httptest.NewRequest(tc.method, "/v1/sandboxes/"+item.ID+tc.suffix, strings.NewReader(tc.body))
			authorization := "Bearer foreign"
			if owner {
				authorization = "Bearer owner"
			}
			request.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, request)
			want := 404
			if owner {
				want = tc.want
			}
			if w.Code != want {
				t.Fatalf("%s owner=%v status=%d body=%s", tc.suffix, owner, w.Code, w.Body.String())
			}
			if !owner && backend.writes != before {
				t.Fatal("foreign request reached Node")
			}
			if owner && (backend.target.Revision != 7 || backend.target.OwnershipGeneration != 2) {
				t.Fatal("target identity lost")
			}
		}
	}
}
