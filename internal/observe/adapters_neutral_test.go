package observe

import "testing"

func TestOpenCodeAndNeutralAdaptersReadJigsOwnFields(t *testing.T) {
	t.Parallel()
	for _, harness := range []string{"opencode", "neutral"} {
		runParseCases(t, harness, []parseCase{
			{harness + " tool", `{"event":"tool","session":"o1","cwd":"/r","tool":"bash","command":"git commit -m x","exit":0,"files":["a.go"],"model":"m"}`,
				Event{Harness: harness, Kind: KindTool, Session: "o1", Cwd: "/r", Tool: "bash", Command: "git commit -m x", Exit: intp(0), Files: []string{"a.go"}, Model: "m"}},
			{harness + " stop", `{"kind":"stop","session":"o1","cwd":"/r","stop_active":true}`,
				Event{Harness: harness, Kind: KindStop, Session: "o1", Cwd: "/r", StopActive: true}},
			{harness + " session end", `{"event":"session-end","session":"o1","cwd":"/r"}`,
				Event{Harness: harness, Kind: KindSessionEnd, Session: "o1", Cwd: "/r"}},
			{harness + " unknown", `{"event":"message.part.updated","session":"o1"}`, Event{}},
		})
		runRenderCases(t, harness, []renderCase{
			{harness + " all three", Event{Kind: KindStop}, Result{Context: "c", Message: "m", Block: "b"}, `{"context":"c","message":"m","block":"b"}`},
			{harness + " only context", Event{Kind: KindTool}, Result{Context: "c"}, `{"context":"c"}`},
			{harness + " nothing", Event{Kind: KindTool}, Result{}, ""},
		})
	}
}

func TestOpenCodePluginHookNamesAndNestedFields(t *testing.T) {
	t.Parallel()
	runParseCases(t, "opencode", []parseCase{
		{"session created", `{"event":"session.created","properties":{"info":{"id":"oc1"}},"directory":"/r"}`,
			Event{Harness: "opencode", Kind: KindSessionStart, Session: "oc1", Cwd: "/r"}},
		{"chat message", `{"event":"chat.message","input":{"sessionID":"oc1"},"directory":"/r"}`,
			Event{Harness: "opencode", Kind: KindPrompt, Session: "oc1", Cwd: "/r"}},
		{"tool executed", `{"event":"tool.execute.after","input":{"tool":"bash","sessionID":"oc1","args":{"command":"ls"}},"output":{"metadata":{"exit":2}},"directory":"/r"}`,
			Event{Harness: "opencode", Kind: KindTool, Session: "oc1", Cwd: "/r", Tool: "bash", Command: "ls", Exit: intp(2)}},
		{"idle", `{"event":"session.idle","properties":{"sessionID":"oc1"},"directory":"/r"}`,
			Event{Harness: "opencode", Kind: KindStop, Session: "oc1", Cwd: "/r"}},
		{"deleted", `{"event":"session.deleted","properties":{"sessionID":"oc1"},"directory":"/r"}`,
			Event{Harness: "opencode", Kind: KindSessionEnd, Session: "oc1", Cwd: "/r"}},
	})
}

// The payloads the OpenCode plugin `jig hooks --harness opencode` writes sends
// (internal/setup/opencode.go): one object per hook, naming the hook.
func TestOpenCodeReadsThePayloadsOfTheShimJigInstalls(t *testing.T) {
	t.Parallel()
	runParseCases(t, "opencode", []parseCase{
		{"session.created", `{"hook":"event","cwd":"/r","event":{"type":"session.created","properties":{"info":{"id":"ses_1","directory":"/r"}}}}`,
			Event{Harness: "opencode", Kind: KindSessionStart, Session: "ses_1", Cwd: "/r"}},
		{"session.idle", `{"hook":"event","cwd":"/r","event":{"type":"session.idle","properties":{"sessionID":"ses_1"}}}`,
			Event{Harness: "opencode", Kind: KindStop, Session: "ses_1", Cwd: "/r"}},
		{"session.deleted", `{"hook":"event","cwd":"/r","event":{"type":"session.deleted","properties":{"info":{"id":"ses_1"}}}}`,
			Event{Harness: "opencode", Kind: KindSessionEnd, Session: "ses_1", Cwd: "/r"}},
		{"chat.message", `{"hook":"chat.message","cwd":"/r","input":{"sessionID":"ses_1","agent":"build"},"output":{"parts":[{"type":"text","text":"go"}]}}`,
			Event{Harness: "opencode", Kind: KindPrompt, Session: "ses_1", Cwd: "/r"}},
		{"tool.execute.after", `{"hook":"tool.execute.after","cwd":"/r","input":{"tool":"bash","sessionID":"ses_1","callID":"c1"},"output":{"title":"git commit","output":"ok","metadata":{"exit":0}},"command":"git commit -m x"}`,
			Event{Harness: "opencode", Kind: KindTool, Session: "ses_1", Cwd: "/r", Tool: "bash", Command: "git commit -m x", Exit: intp(0)}},
		{"another event", `{"hook":"event","cwd":"/r","event":{"type":"message.updated","properties":{}}}`, Event{}},
	})
}

func TestNeutralAdapterTakesAPostCommit(t *testing.T) {
	t.Parallel()
	runParseCases(t, "neutral", []parseCase{
		{"post-commit", `{"event":"post-commit","cwd":"/r","harness":"crew"}`, Event{Harness: "crew", Kind: KindPostCommit, Cwd: "/r"}},
	})
}
