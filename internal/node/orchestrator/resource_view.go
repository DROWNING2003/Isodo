package orchestrator

import nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"

// SandboxResourceSpec exposes the allocation recorded for this specific VM.
// It deliberately does not substitute template or plan defaults.
func (s *LimaServer) SandboxResourceSpec(sandboxID string) *nodev1.ResourceSpec {
	record, ok := s.journal.getSandbox(sandboxID)
	if !ok || record.Resources.CPUs <= 0 || record.Resources.MemoryBytes == 0 {
		return nil
	}
	return &nodev1.ResourceSpec{Vcpu: uint32(record.Resources.CPUs), MemoryBytes: record.Resources.MemoryBytes, DiskBytes: record.Resources.DiskBytes}
}
