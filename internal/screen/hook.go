package screen

import (
	"encoding/json"
	"io"
)

// hookInput is the PreToolUse hook payload read on stdin: the tool being
// called and its input map.
type hookInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// hookOutput is the PreToolUse hook JSON printed on stdout to allow or deny a
// tool call.
type hookOutput struct {
	HookSpecificOutput hookDecision `json:"hookSpecificOutput"`
	SystemMessage      string       `json:"systemMessage,omitempty"`
}

type hookDecision struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// allowReason is the reason attached to an allow decision.
const allowReason = "screened by jig"

// Hook is the PreToolUse hook a screened headless session runs
// (internal/session): it reads one tool call as JSON on stdin, runs it
// through ToolCall, and prints the decision on stdout. A denied call gets a
// deny decision. A passing call to a tool in Granted gets an allow decision,
// which is jig's own only grant for that tool in a headless session; any
// other passing call gets no decision, leaving it to the session's permission
// rules (docs/adr/0008-headless-permission-model.md). Malformed input gets
// no decision either: jig itself grants nothing for a call it cannot even
// parse. That is not the same as denied outright - Claude Code's own
// read-only classifier can still let part of the shell through with no rule
// from jig involved, which is why the session package proves the hook works
// before it relies on it, rather than trusting this alone; an edit tool
// still falls to the session's path-scoped permission rules.
//
// The program that runs it exits 0 whatever Hook printed: the decision is
// communicated through stdout, not the exit code, per the PreToolUse hook
// contract.
func Hook(stdin io.Reader, stdout io.Writer) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return
	}
	var call hookInput
	if err := json.Unmarshal(data, &call); err != nil {
		return
	}

	out := hookOutput{HookSpecificOutput: hookDecision{HookEventName: "PreToolUse"}}
	if reason, ok := ToolCall(call.ToolName, call.ToolInput); !ok {
		out.HookSpecificOutput.PermissionDecision = "deny"
		out.HookSpecificOutput.PermissionDecisionReason = reason
		out.SystemMessage = reason
	} else if Grants(call.ToolName) {
		out.HookSpecificOutput.PermissionDecision = "allow"
		out.HookSpecificOutput.PermissionDecisionReason = allowReason
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
