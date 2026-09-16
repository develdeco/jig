package main

import (
	"encoding/json"
	"io"

	"github.com/develdeco/jig/screen"
)

// screenHookInput is the PreToolUse hook payload jig reads on stdin: the
// tool being called and its input map.
type screenHookInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// screenDenyOutput is the PreToolUse hook JSON jig prints on stdout to deny
// a tool call.
type screenDenyOutput struct {
	HookSpecificOutput screenDenyDetail `json:"hookSpecificOutput"`
	SystemMessage      string           `json:"systemMessage"`
}

type screenDenyDetail struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// cmdScreen implements the hidden `jig _screen` PreToolUse hook verb: read
// one hook call as JSON on stdin, run it through screen.ToolCall, and print
// a deny payload on stdout when it is denied (nothing when it is allowed).
// It always exits 0: denial is communicated through stdout, not the exit
// code, per the PreToolUse hook contract.
func cmdScreen(stdin io.Reader, stdout io.Writer) int {
	runScreen(stdin, stdout)
	return 0
}

// runScreen does the actual work of cmdScreen, factored out so tests can
// drive it directly against in-memory readers/writers.
func runScreen(stdin io.Reader, stdout io.Writer) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return
	}

	var call screenHookInput
	if err := json.Unmarshal(data, &call); err != nil {
		// NOTE: the contract does not define behavior for malformed hook
		// input. jig fails open (no deny output) here, matching the
		// headless backend's "best-effort screening" framing rather than
		// blocking a tool call because the hook payload itself was
		// unparseable.
		return
	}

	reason, ok := screen.ToolCall(call.ToolName, call.ToolInput)
	if ok {
		return
	}

	out := screenDenyOutput{
		HookSpecificOutput: screenDenyDetail{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		},
		SystemMessage: reason,
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return
	}
	stdout.Write(encoded)
	stdout.Write([]byte("\n"))
}
