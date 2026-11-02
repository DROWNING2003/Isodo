// Package runtimecommands holds the narrow Control-to-Node command contract.
// The Guest protocol remains private to the Node; no Guest RPC bytes are tunneled.
package runtimecommands

type Command struct {
	Argv           []string          `json:"argv"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	// RequestID comes exclusively from the Idempotency-Key HTTP header.
	RequestID string `json:"-"`
}

type Request struct {
	SandboxID  string  `json:"sandbox_id"`
	Generation uint64  `json:"generation"`
	Revision   uint64  `json:"revision"`
	Key        string  `json:"key"`
	Command    Command `json:"command"`
}

type Result struct {
	ExecutionID      string `json:"execution_id"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	ExitCode         *int32 `json:"exit_code"`
	Reason           string `json:"reason"`
	OutputIncomplete bool   `json:"output_incomplete"`
}
