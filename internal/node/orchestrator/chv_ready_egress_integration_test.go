package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type readyArtifact struct{ artifact *templateartifact.Artifact }

func (f readyArtifact) Fetch(context.Context, string, string) (string, error) {
	return f.artifact.Directory(), nil
}
func (f readyArtifact) FetchVerified(_ context.Context, _, digest string) (*templateartifact.Artifact, error) {
	if digest != f.artifact.Digest() {
		return nil, os.ErrInvalid
	}
	if err := f.artifact.Check(); err != nil {
		return nil, err
	}
	return f.artifact, nil
}

// Real KVM/CHV component test. Owns its Node state, UUID VM and proxy; never
// attaches to the deployed Node or invokes the workbench/app-link endpoint.
func TestRealCHVReadyEgress(t *testing.T) {
	if os.Getenv("ISODO_CHV_INTEGRATION") != "1" {
		t.Skip("requires isolated Linux/KVM host and verified cached artifact")
	}
	artifactPath := os.Getenv("ISODO_CHV_TEMPLATE_ARTIFACT")
	if artifactPath == "" {
		t.Fatal("ISODO_CHV_TEMPLATE_ARTIFACT required")
	}
	artifact, err := templateartifact.VerifyArtifact(artifactPath, filepath.Base(artifactPath))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "isodo-ready-egress-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("诊断日志保留：%s", dir)
		} else {
			_ = os.RemoveAll(dir)
		}
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ready-without-workbench")) }))
	defer upstream.Close()
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: filepath.Join(dir, "injection"), HostGateway: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	status := testCHVStatus()
	status.NodeID = "isolated-ready-" + uuid.NewString()
	status.TotalMemoryBytes = 4 << 30
	status.AvailableMemoryBytes = 4 << 30
	node, err := NewCHVServer(status, CHVConfig{Directory: filepath.Join(dir, "node"), Binary: os.Getenv("ISODO_CHV_BINARY"), Bucket: "templates", Fetcher: readyArtifact{artifact}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { node.Close(); _ = os.RemoveAll(filepath.Join(dir, "node", "vms")) }()
	node.EnableInjections(manager)
	request := createCHVRequest()
	request.TemplateArtifact.ManifestDigest = artifact.Digest()
	request.TemplateArtifact.Prefix = "templates/sha256/" + artifact.Digest()
	request.Resources.MemoryBytes = 2 << 30
	request.Resources.DiskBytes = uint64(artifact.Manifest().Workspace.MinimumSizeBytes)
	request.Network.EgressMode = nodev1.EgressMode_EGRESS_MODE_ALLOW
	t.Logf("环境：独立 Node + 真实 CHV + 无网卡；Sandbox ID=%s", request.SandboxId)
	apply := func(action nodev1.OperationAction, revision uint64) {
		t.Helper()
		started := time.Now()
		request.Action = action
		request.Revision = revision
		request.OperationId = uuid.NewString()
		request.RequestHash = filepath.Base(artifactPath)
		operation := request
		if action != nodev1.OperationAction_OPERATION_ACTION_CREATE {
			operation = &nodev1.ApplyOperationRequest{OperationId: request.OperationId, SandboxId: request.SandboxId, RequestHash: request.RequestHash, OwnershipGeneration: 1, Revision: revision, Action: action}
		}
		if _, err := node.ApplyOperation(t.Context(), operation); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(150 * time.Second)
		for {
			result, err := node.GetOperation(t.Context(), &nodev1.GetOperationRequest{OperationId: request.OperationId})
			if err != nil {
				t.Fatal(err)
			}
			if terminal(result.State) {
				if result.State != nodev1.OperationState_OPERATION_STATE_SUCCEEDED {
					t.Fatalf("Sandbox ID=%s %s 失败：%s", request.SandboxId, action, result.Message)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("生命周期超时")
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("Sandbox ID=%s 步骤=%s 成功 耗时=%s", request.SandboxId, action, time.Since(started))
	}
	run := func(revision uint64) {
		t.Helper()
		started := time.Now()
		input := runtimecommands.Request{SandboxID: request.SandboxId, Generation: 1, Revision: revision, Key: uuid.NewString(), Command: runtimecommands.Command{Argv: []string{"python3", "-c", `import os,urllib.request; assert os.environ['HTTPS_PROXY']; assert os.path.isfile(os.environ['SSL_CERT_FILE']); print(urllib.request.urlopen('` + upstream.URL + `',timeout=10).read().decode())`}, Env: map[string]string{"NO_PROXY": "", "no_proxy": ""}, Cwd: "/workspace", TimeoutSeconds: 20}}
		response, err := node.RunCommand(t.Context(), wrapperspb.Bytes(mustMarshalCommand(t, input)))
		if err != nil {
			t.Fatal(err)
		}
		var result runtimecommands.Result
		if err = json.Unmarshal(response.Value, &result); err != nil {
			t.Fatal(err)
		}
		if result.ExitCode == nil || *result.ExitCode != 0 || result.Stdout != "ready-without-workbench\n" {
			t.Fatalf("命令失败：%+v", result)
		}
		t.Logf("Sandbox ID=%s 步骤=Commands 首次出站 成功 耗时=%s", request.SandboxId, time.Since(started))
	}
	apply(nodev1.OperationAction_OPERATION_ACTION_CREATE, 1)
	run(1)
	apply(nodev1.OperationAction_OPERATION_ACTION_STOP, 2)
	apply(nodev1.OperationAction_OPERATION_ACTION_START, 3)
	run(3)
	apply(nodev1.OperationAction_OPERATION_ACTION_DELETE, 4)
	if _, err := os.Stat(filepath.Join(dir, "node", "vms", request.SandboxId)); !os.IsNotExist(err) {
		t.Fatalf("VM 目录未清理：%v", err)
	}
	t.Logf("Sandbox ID=%s 清理成功：VM、磁盘、出站桥已删除", request.SandboxId)
}
