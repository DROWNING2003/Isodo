package orchestrator

import (
	"github.com/DROWNING2003/Isodo/internal/node/lima"
	"testing"
)

func TestSandboxResourceSpecUsesRecordedAllocation(t *testing.T) {
	server := &LimaServer{journal: &limaJournal{sandboxes: map[string]sandboxRecord{
		"custom":  {Resources: lima.Resources{CPUs: 6, MemoryBytes: 12 << 30, DiskBytes: 80 << 30}},
		"unknown": {},
	}}}
	result := server.SandboxResourceSpec("custom")
	if result.GetVcpu() != 6 || result.GetMemoryBytes() != 12<<30 || result.GetDiskBytes() != 80<<30 {
		t.Fatalf("incorrect allocation: %v", result)
	}
	if server.SandboxResourceSpec("unknown") != nil || server.SandboxResourceSpec("missing") != nil {
		t.Fatal("unknown allocation must not use plan defaults")
	}
}
