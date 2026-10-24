package templatebuilder

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
)

func TestReadyMustBeUnboundAndMatchManifest(t *testing.T) {
	var manifest templateartifact.Manifest
	manifest.GuestAgent.Version = "test"
	manifest.GuestAgent.Capabilities = []string{"ready.v1"}
	manifest.GuestProtocol.Major = 1
	ready := &guestv1.ReadyResponse{AgentVersion: "test", ProtocolMajor: 1, Capabilities: []string{"ready.v1"}}
	if err := verifyReady(ready, manifest); err != nil {
		t.Fatal(err)
	}
	ready.RuntimeBound = true
	if err := verifyReady(ready, manifest); err == nil {
		t.Fatal("accepted tenant-bound template")
	}
	ready.RuntimeBound = false
	ready.Capabilities = nil
	if err := verifyReady(ready, manifest); err == nil {
		t.Fatal("accepted missing manifest capability")
	}
	ready.Capabilities = []string{"ready.v1"}
	ready.AgentVersion = "unexpected"
	if err := verifyReady(ready, manifest); err == nil {
		t.Fatal("accepted wrong agent version")
	}
}

func TestDiagnosticTailIsBoundedAndPreservesCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serial.log")
	if err := os.WriteFile(path, []byte("discarded-prefix"+strings.Repeat("x", 9000)+"final-error"), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("startup failed")
	err := withLogTail(cause, path)
	if !errors.Is(err, cause) || !strings.HasSuffix(err.Error(), "final-error") || strings.Contains(err.Error(), "discarded-prefix") || len(err.Error()) > 8300 {
		t.Fatalf("unexpected diagnostic tail: length=%d", len(err.Error()))
	}
}
