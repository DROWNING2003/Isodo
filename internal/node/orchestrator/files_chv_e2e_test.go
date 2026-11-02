package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	adminapi "github.com/DROWNING2003/Isodo/internal/api/admin"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	controlnode "github.com/DROWNING2003/Isodo/internal/control/orchestrator"
	"github.com/DROWNING2003/Isodo/internal/control/postgres"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/control/sandboxworker"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/DROWNING2003/Isodo/internal/sandboxfiles"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Full public Files API path: real API keys, isolated PostgreSQL/worker, mTLS
// Node and CHV Guest. No memory store, fake Node or existing VM is used.
func TestFilesPublicAPIRealCHV(t *testing.T) {
	if os.Getenv("ISODO_FILES_CHV_E2E") != "1" {
		t.Skip("requires dedicated migrated PostgreSQL, Linux/KVM and matching Guest artifact")
	}
	ctx := t.Context()
	dbURL := os.Getenv("ISODO_FILES_E2E_DATABASE_URL")
	parsed, err := url.Parse(dbURL)
	if err != nil || !strings.HasPrefix(strings.TrimPrefix(parsed.Path, "/"), "isodo_files_e2e_") {
		t.Fatal("refusing database without isodo_files_e2e_ prefix")
	}
	store, err := postgres.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var database string
	if err := store.Pool().QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || !strings.HasPrefix(database, "isodo_files_e2e_") {
		t.Fatal("dedicated database required")
	}
	var count int
	if err := store.Pool().QueryRow(ctx, "SELECT count(*) FROM sandboxes").Scan(&count); err != nil {
		t.Fatalf("dedicated database schema check: %v", err)
	}
	if count != 0 {
		t.Fatal("E2E requires no existing sandboxes in the dedicated database")
	}
	artifactPath := os.Getenv("ISODO_CHV_TEMPLATE_ARTIFACT")
	artifact, err := templateartifact.VerifyArtifact(artifactPath, filepath.Base(artifactPath))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(os.Getenv("ISODO_FILES_E2E_WORK_DIR"), "isodo-files-chv-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if t.Failed() {
			t.Logf("诊断目录=%s", dir)
		} else {
			_ = os.RemoveAll(dir)
		}
	}()
	files, serverCreds := previewTestTLS(t)
	nodeStatus := testCHVStatus()
	nodeStatus.NodeID = files.NodeID
	nodeStatus.TotalMemoryBytes = 4 << 30
	nodeStatus.AvailableMemoryBytes = 4 << 30
	node, err := NewCHVServer(nodeStatus, CHVConfig{Directory: filepath.Join(dir, "node"), Binary: os.Getenv("ISODO_CHV_BINARY"), Bucket: "templates", Fetcher: readyArtifact{artifact}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		node.Close()
		if err := os.RemoveAll(filepath.Join(dir, "node", "vms")); err != nil {
			t.Errorf("清理 VM 磁盘：%v", err)
		}
	}()
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: filepath.Join(dir, "injections"), HostGateway: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	node.EnableInjections(manager)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rpc := grpc.NewServer(grpc.Creds(serverCreds))
	nodev1.RegisterNodeOrchestratorServer(rpc, node)
	nodev1.RegisterSandboxRuntimeServer(rpc, node)
	RegisterCommandRunner(rpc, node)
	go func() { _ = rpc.Serve(listener) }()
	defer rpc.Stop()
	client, err := controlnode.NewClient(listener.Addr().String(), files)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	worker, err := sandboxworker.New(store, client, files.NodeID, "chv", nil)
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(life) }()
	defer func() { cancel(); <-done }()
	if _, err := store.RegisterSystemTemplate(ctx, "files-e2e", "isolated current Guest artifact", artifact.Digest(), "templates", ""); err != nil {
		t.Fatal(err)
	}
	tenant, err := store.ResolveTenant(ctx, "https://files-e2e.test", uuid.NewString(), "Files E2E")
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, err := store.ResolveTenant(ctx, "https://files-e2e.test", uuid.NewString(), "Foreign")
	if err != nil {
		t.Fatal(err)
	}
	key := func(tenant string, scopes []adminapi.APIKeyScope) string {
		t.Helper()
		idBytes := make([]byte, 16)
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(idBytes); err != nil {
			t.Fatal(err)
		}
		if _, err := rand.Read(secretBytes); err != nil {
			t.Fatal(err)
		}
		id := hex.EncodeToString(idBytes)
		secret := base64.RawURLEncoding.EncodeToString(secretBytes)
		sum := sha256.Sum256([]byte(secret))
		if _, err := store.CreateAPIKey(ctx, tenant, auth.APIKey{ID: id, Name: "isolated-files-e2e", Prefix: "iso_" + id, Scopes: scopes, ExpiresAt: new(time.Now().Add(time.Hour))}, sum[:]); err != nil {
			t.Fatal(err)
		}
		return "iso_" + id + "_" + secret
	}
	owner := key(tenant, []adminapi.APIKeyScope{"sandboxes:read", "sandboxes:write", "sandboxes:execute"})
	foreign := key(foreignTenant, []adminapi.APIKeyScope{"sandboxes:read", "sandboxes:write"})
	readonly := key(tenant, []adminapi.APIKeyScope{"sandboxes:read"})
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(client)
	handler.SetFiles(client)
	handler.SetCommandRunner(client)
	api := httptest.NewServer(httpapi.NewHandler(nil, nil, handler, &auth.Access{Keys: store}))
	defer api.Close()
	httpClient := api.Client()
	httpClient.Timeout = 35 * time.Second
	sandboxID := ""
	started := time.Now()
	request := func(method, p, key string, body []byte, contentType string, want int) []byte {
		t.Helper()
		start := time.Now()
		r, err := http.NewRequestWithContext(ctx, method, api.URL+p, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			r.Header.Set("X-API-Key", key)
		}
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if strings.HasSuffix(p, "/commands/run") {
			r.Header.Set("Idempotency-Key", uuid.NewString())
		}
		response, err := httpClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("Sandbox ID=%s %s %s status=%d want=%d body=%s", sandboxID, method, p, response.StatusCode, want, data)
		}
		if sandboxID == "" && method == "POST" && p == "/v1/sandboxes" {
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(data, &created); err != nil || created.ID == "" {
				t.Fatal("Create response has no Sandbox ID")
			}
			sandboxID = created.ID
		}
		t.Logf("Sandbox ID=%s %s %s 状态=%d 耗时=%s", sandboxID, method, p, response.StatusCode, time.Since(start))
		return data
	}
	decodeSandbox := func(data []byte) sandbox.Sandbox {
		t.Helper()
		var item sandbox.Sandbox
		if err := json.Unmarshal(data, &item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	item := decodeSandbox(request("POST", "/v1/sandboxes", owner, []byte(`{"name":"files-runtime-e2e","template":"files-e2e"}`), "application/json", 202))
	sandboxID = item.ID
	base := "/v1/sandboxes/" + sandboxID
	t.Logf("运行环境=jfcs1024 隔离 PostgreSQL + 真实 CHV + mTLS；Sandbox ID=%s", sandboxID)
	waitState := func(state string) sandbox.Sandbox {
		t.Helper()
		deadline := time.Now().Add(150 * time.Second)
		for {
			item := decodeSandbox(request("GET", base, owner, nil, "", 200))
			if item.ObservedState == state && item.DesiredState == state {
				return item
			}
			if item.ObservedState == "error" {
				t.Fatalf("Sandbox ID=%s 生命周期失败：%+v", sandboxID, item.LastAction)
			}
			if time.Now().After(deadline) {
				t.Fatal("等待 Sandbox 状态超时")
			}
			time.Sleep(time.Second)
		}
	}
	item = waitState("running")
	upload := func(path string, data []byte) {
		t.Helper()
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, err := w.CreateFormFile("file", "data.bin")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(data)
		_ = w.Close()
		request("POST", base+"/files?path="+url.QueryEscape(path), owner, body.Bytes(), w.FormDataContentType(), 200)
	}
	request("POST", base+"/directories", owner, []byte(`{"path":"/workspace/e2e"}`), "application/json", 200)
	text := []byte("persistent text\n")
	binary := bytes.Repeat([]byte{0, 255, 128, 1, 10}, 15000)
	upload("/workspace/e2e/a.txt", text)
	upload("/workspace/e2e/b.bin", binary)
	statPath := base + "/files/stat?path=/workspace/e2e/a.txt"
	var info sandboxfiles.FileMetadata
	if err := json.Unmarshal(request("GET", statPath, owner, nil, "", 200), &info); err != nil || info.SizeBytes != uint64(len(text)) || info.Kind != "regular" {
		t.Fatal("Stat metadata mismatch")
	}
	pagePath := base + "/directories?path=/workspace/e2e&page_size=1"
	var page struct {
		Entries []sandboxfiles.FileMetadata `json:"entries"`
		Next    string                      `json:"next_page_token"`
	}
	if err := json.Unmarshal(request("GET", pagePath, owner, nil, "", 200), &page); err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "a.txt" || page.Next == "" {
		t.Fatal("first directory page mismatch")
	}
	if err := json.Unmarshal(request("GET", pagePath+"&page_token="+url.QueryEscape(page.Next), owner, nil, "", 200), &page); err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "b.bin" || page.Next != "" {
		t.Fatal("second directory page mismatch")
	}
	request("GET", statPath, "", nil, "", 401)
	request("GET", statPath, foreign, nil, "", 404)
	for _, p := range []string{"/workspace/../escape", "/etc/passwd"} {
		request("GET", base+"/files/stat?path="+url.QueryEscape(p), owner, nil, "", 400)
	}
	request("GET", base+"/directories?path=/workspace/e2e&page_token=!!!", owner, nil, "", 400)
	moveBody := []byte(`{"source":"/workspace/e2e/a.txt","destination":"/workspace/e2e/moved.txt"}`)
	request("POST", base+"/files/move", readonly, moveBody, "application/json", 403)
	request("POST", base+"/files/move", foreign, moveBody, "application/json", 404)
	request("POST", base+"/files/move", owner, moveBody, "application/json", 200)
	request("GET", statPath, owner, nil, "", 404)
	if actual := request("GET", base+"/files?path=/workspace/e2e/b.bin", owner, nil, "", 200); !bytes.Equal(actual, binary) {
		t.Fatal("binary changed")
	}
	upload("/workspace/e2e/destination.txt", []byte("old"))
	request("POST", base+"/files/move", owner, []byte(`{"source":"/workspace/e2e/moved.txt","destination":"/workspace/e2e/destination.txt","overwrite":false}`), "application/json", 409)
	request("POST", base+"/files/move", owner, []byte(`{"source":"/workspace/e2e/moved.txt","destination":"/workspace/e2e/destination.txt"}`), "application/json", 200)
	if data := request("GET", base+"/files?path=/workspace/e2e/destination.txt", owner, nil, "", 200); !bytes.Equal(data, text) {
		t.Fatal("Move default overwrite failed")
	}
	request("DELETE", base+"/files?path=/workspace/e2e/b.bin", owner, nil, "", 204)
	request("GET", base+"/files/stat?path=/workspace/e2e/b.bin", owner, nil, "", 404)
	request("DELETE", base+"/files?path=/workspace/e2e/b.bin", owner, nil, "", 204)
	request("DELETE", base+"/files?path=/workspace/e2e", owner, nil, "", 409)
	request("DELETE", base+"/files?path=/workspace/.isodo&recursive=true", owner, nil, "", 403)
	request("DELETE", base+"/files?path=/workspace&recursive=true", owner, nil, "", 403)
	// Commands only creates an adversarial symlink fixture; all file operations above/below use Files HTTP endpoints.
	request("POST", base+"/commands/run", owner, []byte(`{"argv":["python3","-c","import os; os.symlink('.isodo','/workspace/runtime-alias'); os.symlink('/etc','/workspace/outside')"],"timeout_seconds":10}`), "application/json", 200)
	request("DELETE", base+"/files?path=/workspace/runtime-alias/proxy.env", owner, nil, "", 403)
	request("POST", base+"/files/move", owner, []byte(`{"source":"/workspace/e2e/destination.txt","destination":"/workspace/outside/escaped"}`), "application/json", 403)
	// Direct mTLS stale targets must fail as well as public Control guards.
	revision, err := strconv.ParseUint(item.Revision, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stat(ctx, &nodev1.RuntimeReadRequest{Target: &nodev1.SandboxTarget{SandboxId: sandboxID, OwnershipGeneration: 2, Revision: revision}, Path: "/workspace"}); status.Code(err) != codes.NotFound {
		t.Fatalf("stale generation status: %v", err)
	}
	request("POST", base+"/stop", owner, nil, "", 202)
	request("GET", statPath, owner, nil, "", 409)
	waitState("stopped")
	request("GET", base+"/directories?path=/workspace", owner, nil, "", 409)
	request("POST", base+"/start", owner, nil, "", 202)
	waitState("running")
	if _, err := client.Stat(ctx, &nodev1.RuntimeReadRequest{Target: &nodev1.SandboxTarget{SandboxId: sandboxID, OwnershipGeneration: 1, Revision: revision}, Path: "/workspace"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("old revision status after Start: %v", err)
	}
	if data := request("GET", base+"/files?path=/workspace/e2e/destination.txt", owner, nil, "", 200); !bytes.Equal(data, text) {
		t.Fatal("disk contents not retained")
	}
	request("POST", base+"/files/move", owner, []byte(`{"source":"/workspace/e2e","destination":"/workspace/moved-directory"}`), "application/json", 200)
	request("GET", base+"/files/stat?path=/workspace/e2e", owner, nil, "", 404)
	request("DELETE", base+"/files?path=/workspace/moved-directory&recursive=true", owner, nil, "", 204)
	request("POST", base+"/directories", owner, []byte(`{"path":"/workspace/empty"}`), "application/json", 200)
	request("DELETE", base+"/files?path=/workspace/empty", owner, nil, "", 204)
	request("DELETE", base, owner, nil, "", 202)
	request("GET", base+"/directories?path=/workspace", owner, nil, "", 409)
	deadline := time.Now().Add(60 * time.Second)
	for {
		var complete bool
		if err := store.Pool().QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM sandboxes WHERE id=$1::uuid", sandboxID).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delete cleanup timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "node", "vms", sandboxID)); !os.IsNotExist(err) {
		t.Fatal("VM disk directory remains")
	}
	node.mu.Lock()
	_, live := node.live[sandboxID]
	_, egress := node.egress[sandboxID]
	node.mu.Unlock()
	if live || egress {
		t.Fatal("runtime resources remain")
	}
	t.Logf("Sandbox ID=%s Files 公开 API E2E 成功，总耗时=%s；VM/磁盘/出站桥已清理，数据库由运行脚本销毁", sandboxID, time.Since(started))
}
