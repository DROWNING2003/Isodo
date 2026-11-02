package orchestrator

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DROWNING2003/Isodo/internal/node/injection"
)

func TestCodexMCPMergePreservesConfigAndRejectsSymlinks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3.11+ required")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".codex")
	os.Mkdir(dir, 0700)
	path := filepath.Join(dir, "config.toml")
	original := "model = \"fixture-model\"\n[mcp_servers.other]\nurl = \"https://example.com/mcp\"\n"
	os.WriteFile(path, []byte(original), 0600)
	// Host-only component check: substitute the root and avoid changing host UID.
	script := strings.ReplaceAll(codexMCPMerge, "'/workspace'", "'"+root+"'")
	script = "import os\nos.chown=lambda *a,**kw:None\n" + script
	run := func(enabled string) error {
		cmd := exec.Command(python, "-c", script, "31080", enabled)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("merge: %s", output)
		}
		return err
	}
	if err = run("true"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), original) || !strings.Contains(string(data), "http://127.0.0.1:31080/isodo-mcp/github") || strings.Contains(string(data), "githubcopilot") {
		t.Fatalf("wrong merge %s", data)
	}
	if err = run("true"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), "[mcp_servers.isodo_github]") != 1 {
		t.Fatal("duplicate section")
	}
	if err = run("false"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "isodo_github") || !strings.Contains(string(data), "fixture-model") {
		t.Fatal("unbind damaged config")
	}
	os.WriteFile(path, []byte(original+"[mcp_servers.isodo_github]\nurl=\"https://manual.example/mcp\"\n"), 0600)
	if run("true") == nil {
		t.Fatal("manual config overwritten")
	}
	os.Remove(path)
	outside := filepath.Join(root, "outside")
	os.WriteFile(outside, []byte("do-not-change"), 0600)
	os.Symlink(outside, path)
	if run("true") == nil {
		t.Fatal("config symlink followed")
	}
	data, _ = os.ReadFile(outside)
	if string(data) != "do-not-change" {
		t.Fatal("symlink target changed")
	}
}
func TestMCPBindingMaterialStaysOutOfInjectionViews(t *testing.T) {
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	binding := injection.MCPBinding{ID: "fixture-binding", MarketID: "github", Capability: "fixture-control-capability"}
	if err = manager.SetMCP("fixture-sandbox", []injection.MCPBinding{binding}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Get("fixture-sandbox").MCP) != 0 {
		t.Fatal("capability leaked in public injection view")
	}
	if _, err = manager.SetRules("fixture-sandbox", nil); err != nil {
		t.Fatal(err)
	}
	if got := manager.MCP("fixture-sandbox"); len(got) != 1 || got[0] != binding {
		t.Fatal("HTTP credential change erased MCP binding")
	}
}

func TestMCPEncryptedPersistenceAndRecovery(t *testing.T) {
	dir := t.TempDir()
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	cap := "fixture-node-only-capability"
	if err = manager.SetMCP("sandbox", []injection.MCPBinding{{ID: "binding", MarketID: "github", Capability: cap}}); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	data, err := os.ReadFile(filepath.Join(dir, "injections.enc"))
	if err != nil || strings.Contains(string(data), cap) {
		t.Fatal("Node material not encrypted")
	}
	restored, err := injection.NewManager(injection.ManagerConfig{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if b := restored.MCP("sandbox"); len(b) != 1 || b[0].Capability != cap {
		t.Fatal("binding not restored")
	}
	if err = restored.Delete("sandbox"); err != nil {
		t.Fatal(err)
	}
	if len(restored.MCP("sandbox")) != 0 {
		t.Fatal("Node delete retained binding")
	}
}

func TestMCPSessionlessUnsupportedMethodDoesNotStartOAuth(t *testing.T) {
	server := &CHVServer{}
	req := httptest.NewRequest("GET", "http://127.0.0.1:31080/isodo-mcp/github", nil)
	req.Header.Set("Authorization", "Bearer isodo-mcp-placeholder")
	out := httptest.NewRecorder()
	server.serveMCP("sandbox", out, req)
	if out.Code != 405 || out.Header().Get("Allow") != "POST" {
		t.Fatalf("unexpected %d", out.Code)
	}
}
