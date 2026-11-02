package orchestrator

import (
	"context"
	"encoding/json"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/runtimecommands"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const nodeRunCommandMethod = "/isodo.node.v1.CommandRunner/RunCommand"

// RunCommand is the sole Control-side entry to the narrowly scoped Node
// Commands service. Control never opens a Guest management connection.
func (c *Client) RunCommand(ctx context.Context, target *nodev1.SandboxTarget, command sandbox.CommandRequest) (sandbox.CommandResult, error) {
	if err := c.requireService(); err != nil {
		return sandbox.CommandResult{}, status.Error(codes.Unavailable, "Node service is not configured")
	}
	if c.connection == nil {
		return sandbox.CommandResult{}, status.Error(codes.Unavailable, "Node connection is unavailable")
	}
	if target == nil || target.GetSandboxId() == "" || target.GetOwnershipGeneration() == 0 ||
		target.GetRevision() == 0 || command.RequestID == "" {
		return sandbox.CommandResult{}, status.Error(codes.InvalidArgument, "invalid command target or idempotency key")
	}
	envelope := runtimecommands.Request{
		SandboxID: target.GetSandboxId(),
		Generation: target.GetOwnershipGeneration(),
		Revision: target.GetRevision(),
		Key: command.RequestID,
		Command: command,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return sandbox.CommandResult{}, status.Error(codes.InvalidArgument, "command request cannot be encoded")
	}
	var response wrapperspb.BytesValue
	if err := c.connection.Invoke(ctx, nodeRunCommandMethod, wrapperspb.Bytes(encoded), &response); err != nil {
		return sandbox.CommandResult{}, err
	}
	var result sandbox.CommandResult
	if len(response.Value) == 0 || json.Unmarshal(response.Value, &result) != nil ||
		result.ExecutionID == "" || result.Reason == "" {
		return sandbox.CommandResult{}, status.Error(codes.DataLoss, "Node returned an invalid command result")
	}
	return result, nil
}

