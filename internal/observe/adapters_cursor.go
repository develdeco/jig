package observe

// The Cursor adapter. Cursor runs a hook command with a JSON object on stdin
// and reads JSON from stdout. Its payloads have no session id of their own
// kind (a conversation id stands for it) and its hooks are narrower than
// Claude Code: only a session start can hand context to the model, and a
// stop can send a follow-up message that keeps the agent working.
//
//	in   {"hook_event_name","conversation_id","generation_id","workspace_roots","model", ...}
//	     sessionStart         session_id
//	     beforeSubmitPrompt   prompt
//	     afterShellExecution  command, output, duration
//	     afterFileEdit        file_path
//	     stop                 status, loop_count
//	     sessionEnd           reason
//	out  sessionStart: {"additional_context"}
//	     stop:         {"followup_message"}   a new user message, so the agent goes on
//
// What a tool or prompt event would add to the model context has no channel
// in Cursor; it waits for the next one that has. The shape is taken from the
// documentation; only Claude Code is proven end to end.

type cursorShape struct{}

func init() { registerAdapter(cursorShape{}) }

func (cursorShape) Name() string { return "cursor" }

func (a cursorShape) Parse(data []byte) (Event, error) {
	p, err := parsePayload(data)
	if err != nil {
		return Event{}, err
	}
	ev := Event{
		Harness:    a.Name(),
		Session:    p.str("session_id", "conversation_id"),
		Agent:      p.str("agent_id"),
		Cwd:        p.str("cwd"),
		Model:      p.str("model"),
		Transcript: p.str("transcript_path"),
	}
	if ev.Cwd == "" {
		if roots := p.strs("workspace_roots"); len(roots) > 0 {
			ev.Cwd = roots[0]
		}
	}
	switch p.str("hook_event_name") {
	case "sessionStart":
		ev.Kind = KindSessionStart
	case "beforeSubmitPrompt":
		ev.Kind = KindPrompt
	case "afterShellExecution":
		ev.Kind = KindTool
		ev.Tool = "shell"
		ev.Command = p.str("command")
		ev.Exit = p.exitStatus("")
	case "afterFileEdit":
		ev.Kind = KindTool
		ev.Tool = "edit"
		ev.Files = p.editedFiles("")
	case "postToolUse":
		ev.Kind = KindTool
		ev.Tool = p.str("tool_name")
		ev.Command = p.str("tool_input.command", "command")
		ev.Files = p.editedFiles("tool_input")
	case "stop":
		ev.Kind = KindStop
		n, _ := p.integer("loop_count")
		ev.StopActive = n > 0
	case "sessionEnd":
		ev.Kind = KindSessionEnd
	default:
		return Event{}, nil
	}
	return ev, nil
}

func (cursorShape) Render(ev Event, r Result) []byte {
	switch {
	case ev.Kind == KindSessionStart && r.Context != "":
		return jsonLine(struct {
			AdditionalContext string `json:"additional_context"`
		}{r.Context})
	case ev.Kind == KindStop && r.Block != "":
		return jsonLine(struct {
			FollowupMessage string `json:"followup_message"`
		}{r.Block})
	}
	return nil
}
