package main

import (
	"encoding/json"
	"io"

	"github.com/develdeco/jig/internal/screen"
)

// screenHookInput is the PreToolUse hook payload jig reads on stdin: the
// tool being called and its input map.
type screenHookInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// screenDecisionOutput is the PreToolUse hook JSON jig prints on stdout to
// allow or deny a tool call.
type screenDecisionOutput struct {
	HookSpecificOutput screenDecisionDetail `json:"hookSpecificOutput"`
	SystemMessage      string               `json:"systemMessage,omitempty"`
}

type screenDecisionDetail struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// screenAllowReason is the reason attached to an allow decision.
const screenAllowReason = "screened by jig"

// cmdScreen implements the hidden `jig _screen` PreToolUse hook verb: read
// one hook call as JSON on stdin, run it through screen.ToolCall, and print
// the decision on stdout. It always exits 0: the decision is communicated
// through stdout, not the exit code, per the PreToolUse hook contract.
func cmdScreen(stdin io.Reader, stdout io.Writer) int {
	runScreen(stdin, stdout)
	return 0
}

// runScreen does the actual work of cmdScreen, factored out so tests can
// drive it directly against in-memory readers/writers. A denied call gets a
// deny decision. A passing call to a tool in screen.Granted gets an allow
// decision, which is jig's own only grant for that tool in a headless
// session; any other passing call gets no decision, leaving it to the
// session's permission rules (docs/adr/0008-headless-permission-model.md).
func runScreen(stdin io.Reader, stdout io.Writer) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return
	}

	var call screenHookInput
	if err := json.Unmarshal(data, &call); err != nil {
		// Malformed hook input gets no decision: jig itself grants nothing
		// for a call it cannot even parse. That is not the same as denied
		// outright - Claude Code's own read-only classifier can still let
		// part of the shell through with no rule from jig involved, which
		// is why verifyScreen proves the hook works rather than trusting
		// this alone; an edit tool still falls to the session's
		// path-scoped permission rules.
		return
	}

	out := screenDecisionOutput{HookSpecificOutput: screenDecisionDetail{HookEventName: "PreToolUse"}}
	if reason, ok := screen.ToolCall(call.ToolName, call.ToolInput); !ok {
		out.HookSpecificOutput.PermissionDecision = "deny"
		out.HookSpecificOutput.PermissionDecisionReason = reason
		out.SystemMessage = reason
	} else if screen.Grants(call.ToolName) {
		out.HookSpecificOutput.PermissionDecision = "allow"
		out.HookSpecificOutput.PermissionDecisionReason = screenAllowReason
	} else {
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(encoded)
	stdout.Write([]byte("\n"))
}
