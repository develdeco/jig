package setup

// Cursor keeps hooks in ~/.cursor/hooks.json (and <repo>/.cursor/hooks.json):
// {"version": 1, "hooks": {"<event>": [{"command": "..."}]}}, with the entries
// listed directly under the event. afterShellExecution is its shell-command
// event; the payload carries the command and its output. The payload schema is
// the one Cursor documents; only Claude Code's is proven end to end.

// CursorEvents are the events jig adds an entry for in Cursor's hooks.json.
func CursorEvents() []string {
	return []string{"sessionStart", "beforeSubmitPrompt", "afterShellExecution", "stop", "sessionEnd"}
}

var cursorFormat = flatFormat{
	harness:  "cursor",
	events:   CursorEvents(),
	ownerKey: "command",
	build: func(command string) *object {
		e := newObject()
		e.set("command", command)
		return e
	},
	commandKeys: []string{"command"},
	version:     1,
}
