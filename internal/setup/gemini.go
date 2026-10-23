package setup

// The Gemini CLI keeps hooks in the hooks key of its settings.json
// (~/.gemini/settings.json, <repo>/.gemini/settings.json), grouped as Claude
// Code's are, under its own event names: BeforeAgent runs when a prompt is
// submitted, AfterTool after a tool (the shell tool is run_shell_command, and
// the matcher is a regular expression on the tool's name), AfterAgent when the
// agent's turn is over. Each hook may carry a name, which the CLI shows in its
// hooks panel.
//
// The payload schema is the one the Gemini CLI documents; only Claude Code's is
// proven end to end.

// GeminiHooks are the events jig adds an entry for in Gemini's settings.json.
func GeminiHooks() []HookSpec {
	return []HookSpec{
		{Event: "SessionStart"},
		{Event: "BeforeAgent"},
		{Event: "AfterTool", Matcher: "run_shell_command"},
		{Event: "AfterAgent"},
		{Event: "SessionEnd"},
	}
}

var geminiFormat = groupedFormat{
	harness: "gemini",
	hooks:   GeminiHooks(),
	entry: func(command string) *object {
		e := newObject()
		e.set("name", "jig")
		e.set("type", "command")
		e.set("command", command)
		return e
	},
}

func mergeGemini(data []byte, jig string) ([]byte, bool, error) { return geminiFormat.merge(data, jig) }
