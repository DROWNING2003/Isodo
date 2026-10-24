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
	OwnershipGeneration uint64
	Revision            uint64
}

type CreatePayload struct {
	TemplateRef     string `json:"template_ref"`
	TemplateID      string `json:"template_id"`
	TemplateBuildID string `json:"template_build_id"`
	TemplateDigest  string `json:"template_digest"`
	ArtifactBucket  string `json:"artifact_bucket"`
	ArtifactPrefix  string `json:"artifact_prefix"`
}
