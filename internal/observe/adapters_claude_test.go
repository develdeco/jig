package observe

import "testing"

func TestClaudeAdapterParsesEveryEvent(t *testing.T) {
	t.Parallel()
	const common = `"session_id":"s1","transcript_path":"/t/s1.jsonl","cwd":"/work/repo","permission_mode":"default"`
	base := Event{Harness: "claude", Session: "s1", Cwd: "/work/repo", Transcript: "/t/s1.jsonl"}
	with := func(f func(*Event)) Event { e := base; f(&e); return e }
	runParseCases(t, "claude", []parseCase{
		{"session start", `{` + common + `,"hook_event_name":"SessionStart","source":"startup","model":"claude-x"}`,
			with(func(e *Event) { e.Kind, e.Model = KindSessionStart, "claude-x" })},
		{"prompt", `{` + common + `,"hook_event_name":"UserPromptSubmit","prompt":"do it"}`,
			with(func(e *Event) { e.Kind = KindPrompt })},
		{"bash tool", `{` + common + `,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m x"},"tool_response":{"stdout":"ok","stderr":"","interrupted":false}}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Command = KindTool, "Bash", "git commit -m x" })},
		{"bash tool with an exit code", `{` + common + `,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"false"},"tool_response":{"exit_code":1}}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Command, e.Exit = KindTool, "Bash", "false", intp(1) })},
		{"failed bash tool", `{` + common + `,"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"git commit -m x && false"},"error":"exit 1"}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Command = KindTool, "Bash", "git commit -m x && false" })},
		{"edit tool", `{` + common + `,"hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"/work/repo/a.go","old_string":"a","new_string":"b"}}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Files = KindTool, "Edit", []string{"/work/repo/a.go"} })},
		{"subagent tool", `{` + common + `,"agent_id":"a-7","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Command, e.Agent = KindTool, "Bash", "ls", "a-7" })},
		{"stop", `{` + common + `,"hook_event_name":"Stop","stop_hook_active":false}`,
			with(func(e *Event) { e.Kind = KindStop })},
		{"stop while continuing", `{` + common + `,"hook_event_name":"Stop","stop_hook_active":true}`,
			with(func(e *Event) { e.Kind, e.StopActive = KindStop, true })},
		{"session end", `{` + common + `,"hook_event_name":"SessionEnd","reason":"clear"}`,
			with(func(e *Event) { e.Kind = KindSessionEnd })},
		{"an event jig does not act on", `{` + common + `,"hook_event_name":"Notification","message":"hi"}`, Event{}},
	})
}

func TestCodexAdapterReadsTheClaudeShapeUnderItsOwnName(t *testing.T) {
	t.Parallel()
	runParseCases(t, "codex", []parseCase{
		{"tool", `{"session_id":"c1","cwd":"/r","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test"},"model":"gpt-x"}`,
			Event{Harness: "codex", Kind: KindTool, Session: "c1", Cwd: "/r", Tool: "Bash", Command: "go test", Model: "gpt-x"}},
		{"stop", `{"session_id":"c1","cwd":"/r","hook_event_name":"Stop","stop_hook_active":true}`,
			Event{Harness: "codex", Kind: KindStop, Session: "c1", Cwd: "/r", StopActive: true}},
	})
}

func TestClaudeAdapterRenders(t *testing.T) {
	t.Parallel()
	runRenderCases(t, "claude", []renderCase{
		{"session start context", Event{Kind: KindSessionStart}, Result{Context: "here: JIG-1"},
			`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"here: JIG-1"}}`},
		{"prompt context", Event{Kind: KindPrompt}, Result{Context: "notes"},
			`{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"notes"}}`},
		{"tool context", Event{Kind: KindTool}, Result{Context: "notes"},
			`{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"notes"}}`},
		{"stop message", Event{Kind: KindStop}, Result{Message: "jig: 1 running"}, `{"systemMessage":"jig: 1 running"}`},
		{"stop refusal", Event{Kind: KindStop}, Result{Message: "m", Block: "wait"}, `{"decision":"block","reason":"wait","systemMessage":"m"}`},
		{"context where the harness has no channel for it", Event{Kind: KindStop}, Result{Context: "c"}, `{"systemMessage":"c"}`},
		{"nothing to say", Event{Kind: KindTool}, Result{}, ""},
		{"a block that is not on a stop is not one", Event{Kind: KindTool}, Result{Block: "x"}, ""},
	})
}
