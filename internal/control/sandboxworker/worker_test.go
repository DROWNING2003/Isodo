package sandboxworker

import (
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/operation"
)

func TestCreateRequestIncludesCHVDefaults(t *testing.T) {
	job := operation.CreateJob{
		OperationID: "operation", RequestHash: "hash", SandboxID: "sandbox",
		OwnershipGeneration: 1, Revision: 1,
		Resources: operation.Resources{VCPU: 2, MemoryBytes: 512 << 20},
	}

	request := createRequest(job, "chv")

	if request.GetResources().GetVcpu() != 2 || request.GetResources().GetMemoryBytes() != 512<<20 {
		t.Fatalf("resources = %v, want 2 vCPU and 512 MiB", request.GetResources())
	}
	if request.GetNetwork().GetEgressMode() != nodev1.EgressMode_EGRESS_MODE_ALLOW {
		t.Fatalf("egress mode = %v, want allow", request.GetNetwork().GetEgressMode())
	}
}
