package orchestrator

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestT3WorkbenchRestartProcessSafety(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux pidfd process regression; run t3_restart_test.py on the validation host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), python, "t3_restart_test.py").CombinedOutput(); err != nil {
		t.Fatalf("T3 restart process regression: %v\n%s", err, out)
	}
}
