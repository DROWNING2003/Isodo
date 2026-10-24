package templatebuilder

import "context"

// ProbeRuntimeCommand executes only the fixed operator validation script. It
// neither accepts arbitrary commands nor advertises a public Exec capability.
func ProbeRuntimeCommand(ctx context.Context, instance *Runtime, create bool) (string, error) {
	return warmExec(ctx, instance.VsockSocket(), instance.Identity(), create)
}
