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

func TestNeutralAdapterTakesAPostCommit(t *testing.T) {
	t.Parallel()
	runParseCases(t, "neutral", []parseCase{
		{"post-commit", `{"event":"post-commit","cwd":"/r","harness":"crew"}`, Event{Harness: "crew", Kind: KindPostCommit, Cwd: "/r"}},
	})
}
