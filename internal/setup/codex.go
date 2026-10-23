package setup

// Codex CLI keeps its hooks in ~/.codex/hooks.json (and <repo>/.codex/hooks.json),
// in the shape Claude Code's settings use: hooks.<Event> is a list of groups,
// each with an optional matcher and a list of command hooks. Codex has no
// session-end event, so a session there ends when the next one starts or the
// ledger's own tail says so; its shell tool is reported to hooks as Bash.
//
// Codex reads hooks only when its hooks feature is on (the codex_hooks flag in
// ~/.codex/config.toml); jig does not edit that file, and says so after an
// install. The payload schema is the one Codex documents; only Claude Code's is
// proven end to end.

// CodexHooks are the events jig adds an entry for in Codex's hooks.json.
func CodexHooks() []HookSpec {
	return []HookSpec{
		{Event: "SessionStart"},
		{Event: "UserPromptSubmit"},
		{Event: "PostToolUse", Matcher: "Bash"},
		{Event: "Stop"},
	}
}

var codexFormat = groupedFormat{
	harness: "codex",
	hooks:   CodexHooks(),
	entry: func(command string) *object {
		e := newObject()
		e.set("type", "command")
		e.set("command", command)
		return e
	},
}

func mergeCodex(data []byte, jig string) ([]byte, bool, error) { return codexFormat.merge(data, jig) }
