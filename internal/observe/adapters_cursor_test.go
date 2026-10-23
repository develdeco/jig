package observe

import "testing"

func TestCursorAdapter(t *testing.T) {
	t.Parallel()
	const common = `"conversation_id":"conv1","generation_id":"g1","workspace_roots":["/r","/other"],"model":"m"`
	base := Event{Harness: "cursor", Session: "conv1", Cwd: "/r", Model: "m"}
	with := func(f func(*Event)) Event { e := base; f(&e); return e }
	runParseCases(t, "cursor", []parseCase{
		{"session start", `{` + common + `,"hook_event_name":"sessionStart","session_id":"sess1"}`,
			with(func(e *Event) { e.Kind, e.Session = KindSessionStart, "sess1" })},
		{"prompt", `{` + common + `,"hook_event_name":"beforeSubmitPrompt","prompt":"go"}`,
			with(func(e *Event) { e.Kind = KindPrompt })},
		{"shell", `{` + common + `,"hook_event_name":"afterShellExecution","command":"git commit -m x","output":"ok","duration":12}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Command = KindTool, "shell", "git commit -m x" })},
		{"file edit", `{` + common + `,"hook_event_name":"afterFileEdit","file_path":"/r/a.go","edits":[]}`,
			with(func(e *Event) { e.Kind, e.Tool, e.Files = KindTool, "edit", []string{"/r/a.go"} })},
		{"stop", `{` + common + `,"hook_event_name":"stop","status":"completed","loop_count":0}`,
			with(func(e *Event) { e.Kind = KindStop })},
		{"stop after a follow-up", `{` + common + `,"hook_event_name":"stop","status":"completed","loop_count":2}`,
			with(func(e *Event) { e.Kind, e.StopActive = KindStop, true })},
		{"session end", `{` + common + `,"hook_event_name":"sessionEnd","reason":"done"}`,
			with(func(e *Event) { e.Kind = KindSessionEnd })},
		{"another hook", `{` + common + `,"hook_event_name":"beforeReadFile"}`, Event{}},
	})
	runRenderCases(t, "cursor", []renderCase{
		{"session start", Event{Kind: KindSessionStart}, Result{Context: "c"}, `{"additional_context":"c"}`},
		{"a stop that goes on", Event{Kind: KindStop}, Result{Block: "wait", Message: "m"}, `{"followup_message":"wait"}`},
		{"a prompt cannot be told anything", Event{Kind: KindPrompt}, Result{Context: "c"}, ""},
		{"a tool call cannot either", Event{Kind: KindTool}, Result{Context: "c"}, ""},
		{"a stop with only a message", Event{Kind: KindStop}, Result{Message: "m"}, ""},
	})
}
