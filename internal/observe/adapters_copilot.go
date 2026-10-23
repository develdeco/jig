package observe

// The GitHub Copilot adapter. Copilot runs a hook command with a JSON object
// on stdin. Its payloads are camelCase, name no event (the hook file does),
// and carry no session id in the versions documented, so the adapter takes
// the event from a name when the call gives one and otherwise from the fields
// the payload has:
//
//	sessionStart          source, initialPrompt
//	userPromptSubmitted   prompt
//	postToolUse           toolName, toolArgs (a JSON string), toolResult
//	sessionEnd            reason
//	every one             timestamp, cwd, and sessionId when the version sends it
//
// Copilot reads nothing from the stdout of these hooks (only preToolUse may
// answer), so the adapter prints nothing: a session of Copilot is recorded
// and its commits are verified, and what jig would say to the agent waits for
// a harness that can be told. The shape is taken from the documentation; only
// Claude Code is proven end to end.

type copilotShape struct{}

func init() { registerAdapter(copilotShape{}) }

func (copilotShape) Name() string { return "copilot" }

func (a copilotShape) Parse(data []byte) (Event, error) {
	p, err := parsePayload(data)
	if err != nil {
		return Event{}, err
	}
	ev := Event{
		Harness:    a.Name(),
		Session:    p.str("sessionId", "session_id", "session"),
		Cwd:        p.str("cwd"),
		Model:      p.str("model"),
		Transcript: p.str("transcriptPath", "transcript_path"),
	}
	switch normalizeName(p.str("hook_event_name", "hookEventName", "event", "hook")) {
	case "sessionstart":
		ev.Kind = KindSessionStart
	case "userpromptsubmitted", "userpromptsubmit", "prompt":
		ev.Kind = KindPrompt
	case "posttooluse", "tool":
		ev.Kind = KindTool
	case "agentstop", "stop":
		ev.Kind = KindStop
	case "sessionend":
		ev.Kind = KindSessionEnd
	case "":
		// The call named no event: the payload's fields say which it is.
		switch {
		case p.str("toolName", "tool_name") != "":
			ev.Kind = KindTool
		case p.str("prompt") != "":
			ev.Kind = KindPrompt
		case p.str("initialPrompt", "source") != "":
			ev.Kind = KindSessionStart
		case p.str("reason") != "":
			ev.Kind = KindSessionEnd
		default:
			return Event{}, nil
		}
	default:
		return Event{}, nil
	}
	if ev.Kind == KindTool {
		ev.Tool = p.str("toolName", "tool_name")
		args := p.object("toolArgs", "tool_input", "toolInput")
		ev.Command = args.str("command")
		ev.Files = args.editedFiles("")
		ev.Exit = args.exitStatus("")
	}
	return ev, nil
}

func (copilotShape) Render(Event, Result) []byte { return nil }
