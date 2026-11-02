package sandbox

import (
	"context"
	"net/http"
	"time"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	publicapi "github.com/DROWNING2003/Isodo/internal/api/public"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultCommandTimeoutSeconds = 60
	maxCommandTimeoutSeconds     = 300
)

// CommandRequest is a non-interactive Guest execution request.
// The idempotency key is accepted only as an HTTP header.
type CommandRequest = runtimecommands.Command
type CommandResult = runtimecommands.Result

type CommandRunner interface {
	RunCommand(context.Context, *nodev1.SandboxTarget, CommandRequest) (CommandResult, error)
}

func (h *Handler) SetCommandRunner(runner CommandRunner) {
	h.commands = runner
}

// RunSandboxCommand is the public, authenticated Runtime API. The only command
// executor is the sandbox Guest Agent reached through a fenced Node relay.
func (h *Handler) RunSandboxCommand(w http.ResponseWriter, r *http.Request, sandboxID string, _ publicapi.RunSandboxCommandParams) {
	target, item, ok := h.resolveRuntimeTargetWithSandbox(w, r, sandboxID, false)
	if !ok {
		return
	}
	if item.ObservedState != "running" || item.DesiredState != "running" || item.Deletion != nil {
		writeError(w, http.StatusConflict, "SANDBOX_NOT_RUNNING", "Commands require a running sandbox.")
		return
	}
	if h.commands == nil {
		writeError(w, http.StatusServiceUnavailable, "COMMANDS_UNAVAILABLE", "Sandbox command execution is not configured.")
		return
	}
	var input CommandRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Argv) == 0 || len(input.Argv) > 256 || input.Argv[0] == "" || input.TimeoutSeconds < 0 || input.TimeoutSeconds > maxCommandTimeoutSeconds {
		writeError(w, http.StatusBadRequest, "INVALID_COMMAND", "argv must contain 1-256 arguments and timeout_seconds must be between 0 and 300.")
		return
	}
	if input.TimeoutSeconds == 0 {
		input.TimeoutSeconds = defaultCommandTimeoutSeconds
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !validCommandKey(keys[0]) {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Supply one Idempotency-Key with 8-128 safe ASCII characters.")
		return
	}
	input.RequestID = keys[0]
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(input.TimeoutSeconds+10)*time.Second)
	defer cancel()
	result, err := h.commands.RunCommand(ctx, target, input)
	if err != nil {
		code, label, message := http.StatusServiceUnavailable, "COMMAND_UNAVAILABLE", "The sandbox command could not be completed."
		switch status.Code(err) {
		case codes.InvalidArgument:
			code, label, message = http.StatusBadRequest, "INVALID_COMMAND", "The command arguments or environment are invalid."
		case codes.NotFound:
			code, label, message = http.StatusNotFound, "COMMAND_NOT_FOUND", "The command or sandbox runtime could not be found."
		case codes.FailedPrecondition, codes.Aborted:
			code, label, message = http.StatusConflict, "SANDBOX_STATE_CONFLICT", "The sandbox runtime changed while executing the command."
		case codes.OutOfRange:
			code, label, message = http.StatusRequestEntityTooLarge, "COMMAND_OUTPUT_LIMIT", "The command output exceeded the permitted limit."
		case codes.ResourceExhausted:
			code, label, message = http.StatusTooManyRequests, "SANDBOX_PROCESS_LIMIT", "The sandbox has reached its process or execution limit."
		case codes.AlreadyExists:
			code, label, message = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used for a different command."
		case codes.DeadlineExceeded:
			code, label, message = http.StatusGatewayTimeout, "COMMAND_DEADLINE", "The command execution exceeded its deadline."
		case codes.DataLoss:
			code, label, message = http.StatusBadGateway, "COMMAND_OUTPUT_INCOMPLETE", "Some command output was lost; the result is not complete."
		case codes.Unimplemented:
			code, label, message = http.StatusNotImplemented, "COMMANDS_UNSUPPORTED", "This runtime does not support commands."
		}
		writeError(w, code, label, message)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

// validCommandKey accepts stable client-generated tokens without control characters.
// The Node independently validates the same rule before any Guest RPC.
func validCommandKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, ch := range []byte(value) {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return false
	}
	return true
}
