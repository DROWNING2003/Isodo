package orchestrator

import (
	"github.com/DROWNING2003/Isodo/internal/skillartifact"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillInstallerIsolationRemovalAndFencing(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	root := t.TempDir()
	script := "import os\nos.chown=lambda *a,**kw:None\n" + strings.ReplaceAll(skillInstall, "'/workspace'", "'"+root+"'")
	run := func(data, version, revision string) error {
		cmd := exec.CommandContext(t.Context(), python, "-c", script, data, version, "1", revision)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Log(string(out))
		}
		return err
	}
	const id = "11111111-1111-4111-8111-111111111111"
	payload, err := skillartifact.Encode([]skillartifact.Skill{{ID: id, Name: "fixture-review", Description: "fixture", Body: "Use evidence; $(touch /tmp/never-run)"}})
	if err != nil {
		t.Fatal(err)
	}
	data := string(payload)
	if err = run(data, "1", "1"); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, ".agents", "skills")
	file := filepath.Join(base, "isodo-"+id, "SKILL.md")
	contents, _ := os.ReadFile(file)
	if !strings.Contains(string(contents), "$(touch") {
		t.Fatal("content not installed literally")
	}
	if err = run(data, "1", "1"); err != nil {
		t.Fatal("idempotent retry", err)
	}
	manual := filepath.Join(base, "manual")
	os.Mkdir(manual, 0700)
	os.WriteFile(filepath.Join(manual, "SKILL.md"), []byte("keep"), 0600)
	if err = run("[]", "2", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("removed skill remained")
	}
	if _, err = os.Stat(filepath.Join(manual, "SKILL.md")); err != nil {
		t.Fatal("manual skill damaged")
	}
	if run(data, "1", "1") == nil {
		t.Fatal("stale version accepted")
	}
	if err = run(data, "3", "2"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(file, []byte("user edit"), 0600)
	if run("[]", "4", "2") == nil {
		t.Fatal("user edited file deleted")
	}
	os.Remove(file)
	outside := filepath.Join(root, "outside")
	os.WriteFile(outside, []byte("keep"), 0600)
	os.Symlink(outside, file)
	if run(data, "4", "2") == nil {
		t.Fatal("symlink accepted")
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "keep" {
		t.Fatal("outside changed")
	}
}
func TestSkillInstallerRejectsParentSymlink(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	root := t.TempDir()
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, ".agents"))
	script := strings.ReplaceAll(skillInstall, "'/workspace'", "'"+root+"'")
	if out, err := exec.CommandContext(t.Context(), python, "-c", script, "[]", "1", "1", "1").CombinedOutput(); err == nil {
		t.Fatal("symlink parent accepted", string(out))
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("outside modified")
	}
}
