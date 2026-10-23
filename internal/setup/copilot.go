package setup

import "strings"

// GitHub Copilot reads hooks from JSON files in a directory of hooks: the user
// directory ~/.copilot/hooks/ and, for a repository, <repo>/.github/hooks/. jig
// writes one file there, jig.json, so it never shares a file with the person's
// own hooks. The shape is {"version": 1, "hooks": {"<event>": [{"type":
// "command", "bash": "...", "powershell": "..."}]}}. The payload schema is the
// one GitHub documents; only Claude Code's is proven end to end.

// CopilotEvents are the events jig adds an entry for in Copilot's jig.json.
func CopilotEvents() []string {
	return []string{"sessionStart", "userPromptSubmitted", "postToolUse", "agentStop", "sessionEnd"}
}

// copilotTimeout is how long, in seconds, Copilot gives one jig hook.
const copilotTimeout = 30

var copilotFormat = flatFormat{
	harness:  "copilot",
	events:   CopilotEvents(),
	ownerKey: "bash",
	build: func(command string) *object {
		e := newObject()
		e.set("type", "command")
		e.set("bash", command)
		e.set("powershell", powershellCommand(command))
		e.set("timeoutSec", jsonInt(copilotTimeout))
		e.set("comment", "jig")
		return e
	},
	commandKeys: []string{"bash", "powershell"},
	version:     1,
}

// powershellCommand is command as PowerShell runs it: a program in quotes is
// called with the call operator, since a quoted string alone is only a string.
func powershellCommand(command string) string {
	if strings.HasPrefix(command, `"`) {
		return "& " + command
	}
	return command
}
