package observe

// The neutral adapter, and OpenCode's, which sends the neutral shape. The
// neutral shape is jig's own fields as one JSON object, for a script or a
// plugin that builds the event itself, and what jig answers in the same
// words:
//
//	in   {"event"|"kind", "session", "agent", "cwd", "tool", "command", "exit",
//	      "files", "model", "transcript", "stop_active", "harness"}
//	out  {"context", "message", "block"}   each only when it has something to say
//
// event takes jig's kinds (session-start, prompt, tool, stop, session-end,
// post-commit) and, for OpenCode, the names of its plugin hooks (session.created,
// chat.message, tool.execute.after, session.idle, session.deleted). The
// OpenCode plugin jig installs pipes its hook's input and output in as
// "input" and "output" objects, and the reads below look there too for the
// session, the tool and its command. Nothing here is proven against a real
// OpenCode.

type neutralShape struct{ name string }

func init() {
	registerAdapter(neutralShape{"neutral"})
	registerAdapter(neutralShape{"opencode"})
}

func (a neutralShape) Name() string { return a.name }

func (a neutralShape) Parse(data []byte) (Event, error) {
	p, err := parsePayload(data)
	if err != nil {
		return Event{}, err
	}
	ev := Event{
		Harness: a.name,
		Session: p.str("session", "session_id", "sessionID", "sessionId", "input.sessionID", "properties.sessionID", "properties.info.id"),
		Agent:   p.str("agent", "agent_id"),
		Cwd:     p.str("cwd", "directory", "worktree", "properties.directory"),
		Tool:    p.str("tool", "tool_name", "input.tool"),
		Command: p.str("command", "args.command", "input.args.command", "output.args.command", "tool_input.command"),
		Files:   p.strs("files"),
		Model:   p.str("model", "properties.model"),

		Transcript: p.str("transcript", "transcript_path"),
		StopActive: p.flag("stop_active", "stop_hook_active"),
	}
	if a.name == "neutral" {
		if h := p.str("harness"); h != "" {
			ev.Harness = h
		}
	}
	if n, ok := p.integer("exit", "exit_code", "metadata.exit", "output.metadata.exit"); ok {
		ev.Exit = &n
	}
	switch normalizeName(p.str("event", "kind", "type", "hook", "hook_event_name")) {
	case "sessionstart", "sessioncreated":
		ev.Kind = KindSessionStart
	case "prompt", "userpromptsubmit", "chatmessage":
		ev.Kind = KindPrompt
	case "tool", "posttooluse", "toolexecuteafter":
		ev.Kind = KindTool
	case "stop", "sessionidle":
		ev.Kind = KindStop
	case "sessionend", "sessiondeleted":
		ev.Kind = KindSessionEnd
	case "postcommit":
		ev.Kind = KindPostCommit
	default:
		return Event{}, nil
	}
	return ev, nil
}

// neutralOut is what jig answers a neutral call with.
type neutralOut struct {
	Context string `json:"context,omitempty"`
	Message string `json:"message,omitempty"`
	Block   string `json:"block,omitempty"`
}

func (neutralShape) Render(_ Event, r Result) []byte {
	if r.Empty() {
		return nil
	}
	return jsonLine(neutralOut{Context: r.Context, Message: r.Message, Block: r.Block})
}
