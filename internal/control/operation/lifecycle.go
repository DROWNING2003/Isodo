package operation

import "errors"

var ErrNoLifecycleOperation = errors.New("no sandbox lifecycle operation")

type LifecyclePayload struct {
	Revision            uint64 `json:"revision"`
	OwnershipGeneration uint64 `json:"ownership_generation"`
}

type LifecycleJob struct {
	OperationID         string
	WorkerID            string
	SandboxID           string
	TenantID            string
	RequestHash         string
	Action              string
	Revision            uint64
	OwnershipGeneration uint64
}
