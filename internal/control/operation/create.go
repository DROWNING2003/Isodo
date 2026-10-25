package operation

import "errors"

var ErrNoCreateOperation = errors.New("no sandbox create operation is available")

type CreateJob struct {
	OperationID         string
	WorkerID            string
	SandboxID           string
	TenantID            string
	RequestHash         string
	TemplateRef         string
	TemplateID          string
	TemplateBuildID     string
	TemplateDigest      string
	ArtifactBucket      string
	ArtifactPrefix      string
	Resources           Resources
	OwnershipGeneration uint64
	Revision            uint64
}

type Resources struct {
	VCPU        uint32 `json:"vcpu"`
	MemoryBytes uint64 `json:"memory_bytes"`
}

const (
	DefaultVCPU        = 2
	DefaultMemoryBytes = 2 << 30
)

func DefaultResources() Resources {
	return Resources{VCPU: DefaultVCPU, MemoryBytes: DefaultMemoryBytes}
}

type CreatePayload struct {
	TemplateRef     string     `json:"template_ref"`
	TemplateID      string     `json:"template_id"`
	TemplateBuildID string     `json:"template_build_id"`
	TemplateDigest  string     `json:"template_digest"`
	ArtifactBucket  string     `json:"artifact_bucket"`
	ArtifactPrefix  string     `json:"artifact_prefix"`
	Resources       *Resources `json:"resources,omitempty"`
}
