package orchestrator

import _ "embed"

// t3WorkbenchRestartProgram runs inside the Guest. Both arguments are fixed by
// the Node, and pidfds keep escalation bound to the verified T3 process.
//
//go:embed t3_restart.py
var t3WorkbenchRestartProgram string
