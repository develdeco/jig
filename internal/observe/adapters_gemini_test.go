package observe

import "testing"

func TestGeminiAdapter(t *testing.T) {
	t.Parallel()
	runParseCases(t, "gemini", []parseCase{
		{"session start", `{"session_id":"g1","cwd":"/r","hook_event_name":"SessionStart","source":"startup"}`,
			Event{Harness: "gemini", Kind: KindSessionStart, Session: "g1", Cwd: "/r"}},
		{"prompt", `{"session_id":"g1","cwd":"/r","hook_event_name":"BeforeAgent","prompt":"go"}`,
			Event{Harness: "gemini", Kind: KindPrompt, Session: "g1", Cwd: "/r"}},
		{"shell tool", `{"session_id":"g1","cwd":"/r","hook_event_name":"AfterTool","tool_name":"run_shell_command","tool_input":{"command":"git commit -m x"},"tool_response":{"llmContent":"ok"}}`,
			Event{Harness: "gemini", Kind: KindTool, Session: "g1", Cwd: "/r", Tool: "run_shell_command", Command: "git commit -m x"}},
		{"shell tool with a description", `{"session_id":"g1","cwd":"/r","hook_event_name":"AfterTool","tool_name":"run_shell_command","tool_input":{"command":"go test","description":"Run the tests"}}`,
			Event{Harness: "gemini", Kind: KindTool, Session: "g1", Cwd: "/r", Tool: "run_shell_command", Command: "go test", Description: "Run the tests"}},
		{"file tool", `{"session_id":"g1","cwd":"/r","hook_event_name":"AfterTool","tool_name":"write_file","tool_input":{"file_path":"/r/a.go","content":"x"}}`,
			Event{Harness: "gemini", Kind: KindTool, Session: "g1", Cwd: "/r", Tool: "write_file", Files: []string{"/r/a.go"}}},
		{"agent done", `{"session_id":"g1","cwd":"/r","hook_event_name":"AfterAgent","stop_hook_active":true}`,
			Event{Harness: "gemini", Kind: KindStop, Session: "g1", Cwd: "/r", StopActive: true}},
		{"session end", `{"session_id":"g1","cwd":"/r","hook_event_name":"SessionEnd","reason":"exit"}`,
			Event{Harness: "gemini", Kind: KindSessionEnd, Session: "g1", Cwd: "/r"}},
		{"another event", `{"session_id":"g1","hook_event_name":"BeforeModel"}`, Event{}},
	})
	runRenderCases(t, "gemini", []renderCase{
		{"session start", Event{Kind: KindSessionStart}, Result{Context: "c"}, `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"c"}}`},
		{"prompt", Event{Kind: KindPrompt}, Result{Context: "c"}, `{"hookSpecificOutput":{"hookEventName":"BeforeAgent","additionalContext":"c"}}`},
		{"tool", Event{Kind: KindTool}, Result{Context: "c"}, `{"hookSpecificOutput":{"hookEventName":"AfterTool","additionalContext":"c"}}`},
		{"the agent is sent back to work", Event{Kind: KindStop}, Result{Block: "wait"}, `{"decision":"deny","reason":"wait"}`},
	})
}
