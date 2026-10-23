package observe

// The Gemini CLI adapter. Gemini CLI runs a hook command with a JSON object
// on stdin and reads JSON from stdout, in the shape Claude Code made familiar
// with other event names:
//
//	in   {"session_id","transcript_path","cwd","hook_event_name","timestamp", ...}
//	     SessionStart  source
//	     BeforeAgent   prompt                               (the person sent a prompt)
//	     AfterTool     tool_name, tool_input, tool_response (a tool finished)
//	     AfterAgent    prompt, prompt_response, stop_hook_active (the agent is done)
//	     SessionEnd    reason
//	out  {"hookSpecificOutput":{"hookEventName","additionalContext"}}  context
//	     {"systemMessage"}                                             for the person
//	     {"decision":"deny","reason"}   AfterAgent: reject the answer and keep working
//
// The shape is taken from the CLI documentation; only Claude Code is proven
// end to end against the real program.

type geminiShape struct{}

func init() { registerAdapter(geminiShape{}) }

func (geminiShape) Name() string { return "gemini" }

func (a geminiShape) Parse(data []byte) (Event, error) {
	p, err := parsePayload(data)
	if err != nil {
		return Event{}, err
	}
	ev := Event{
		Harness:    a.Name(),
		Session:    p.str("session_id"),
		Agent:      p.str("agent_id"),
		Cwd:        p.str("cwd"),
		Model:      p.str("model"),
		Transcript: p.str("transcript_path"),
	}
	switch p.str("hook_event_name") {
	case "SessionStart":
		ev.Kind = KindSessionStart
	case "BeforeAgent":
		ev.Kind = KindPrompt
	case "AfterTool":
		ev.Kind = KindTool
		ev.Tool = p.str("tool_name")
		ev.Command = p.str("tool_input.command")
		ev.Files = p.editedFiles("tool_input")
		ev.Exit = p.exitStatus("tool_response")
	case "AfterAgent":
		ev.Kind = KindStop
		ev.StopActive = p.flag("stop_hook_active")
	case "SessionEnd":
		ev.Kind = KindSessionEnd
	default:
		return Event{}, nil
	}
	return ev, nil
}

var geminiHookEvents = map[string]string{
	KindSessionStart: "SessionStart",
	KindPrompt:       "BeforeAgent",
	KindTool:         "AfterTool",
}

func (geminiShape) Render(ev Event, r Result) []byte {
	return renderHookOutput(ev, r, geminiHookEvents, "deny")
}
