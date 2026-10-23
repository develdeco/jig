package observe

import "testing"

func TestCopilotAdapter(t *testing.T) {
	t.Parallel()
	runParseCases(t, "copilot", []parseCase{
		{"session start by its fields", `{"timestamp":1,"cwd":"/r","source":"new","initialPrompt":"hi"}`,
			Event{Harness: "copilot", Kind: KindSessionStart, Cwd: "/r"}},
		{"prompt by its fields", `{"timestamp":1,"cwd":"/r","prompt":"do it"}`,
			Event{Harness: "copilot", Kind: KindPrompt, Cwd: "/r"}},
		{"bash tool by its fields", `{"timestamp":1,"cwd":"/r","toolName":"bash","toolArgs":"{\"command\":\"git commit -m x\"}","toolResult":{"resultType":"success","textResultForLlm":"ok"}}`,
			Event{Harness: "copilot", Kind: KindTool, Cwd: "/r", Tool: "bash", Command: "git commit -m x"}},
		{"edit tool with object arguments", `{"cwd":"/r","toolName":"edit","toolArgs":{"path":"/r/a.go"}}`,
			Event{Harness: "copilot", Kind: KindTool, Cwd: "/r", Tool: "edit", Files: []string{"/r/a.go"}}},
		{"session end by its fields", `{"cwd":"/r","reason":"complete"}`,
			Event{Harness: "copilot", Kind: KindSessionEnd, Cwd: "/r"}},
		{"a named event wins, and a session id is read when sent", `{"hookEventName":"sessionEnd","sessionId":"cp1","cwd":"/r","prompt":"x"}`,
			Event{Harness: "copilot", Kind: KindSessionEnd, Session: "cp1", Cwd: "/r"}},
		{"nothing to go by", `{"cwd":"/r"}`, Event{}},
	})
	runRenderCases(t, "copilot", []renderCase{
		{"copilot reads no answer", Event{Kind: KindSessionStart}, Result{Context: "c", Message: "m", Block: "b"}, ""},
	})
}
