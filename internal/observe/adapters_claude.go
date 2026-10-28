package observe

import "path/filepath"

// The Claude Code adapter, and Codex's, which sends the same shape. Claude
// Code runs a hook command with a JSON object on stdin and reads JSON from
// its stdout:
//
//	in   {"session_id","transcript_path","cwd","hook_event_name", ...}
//	     SessionStart      source, model
//	     UserPromptSubmit  prompt
//	     PostToolUse       tool_name, tool_input.command|file_path, tool_response
//	     PostToolUseFailure  the same, for a tool call that failed
//	     Stop              stop_hook_active
//	     SessionEnd        reason
//	     agent_id          present when a subagent is speaking
//	out  {"hookSpecificOutput":{"hookEventName","additionalContext"}}   context
//	     {"systemMessage"}                                              for the person
//	     {"decision":"block","reason"}                                  keep working (Stop)
//	env  CLAUDE_ENV_FILE (SessionStart): a file of the session's, in a
//	     directory named after its id, that the hook appends export lines
//	     to; Claude Code sources it before each Bash command
//
// Claude Code puts the stdout of a SessionStart or UserPromptSubmit hook that
// exits 0 into the model context as plain text, so a hook must print
// nothing but its JSON.

// claudeShape is the claude adapter, and codex's: envFile says whether the
// harness gives a SessionStart hook a CLAUDE_ENV_FILE, which only Claude
// Code does.
type claudeShape struct {
	name    string
	envFile bool
}

func init() {
	registerAdapter(claudeShape{name: "claude", envFile: true})
	registerAdapter(claudeShape{name: "codex"})
}

func (a claudeShape) Name() string { return a.name }

func (a claudeShape) Parse(data []byte) (Event, error) {
	p, err := parsePayload(data)
	if err != nil {
		return Event{}, err
	}
	ev := Event{
		Harness:    a.name,
		Session:    p.str("session_id"),
		Agent:      p.str("agent_id"),
		Cwd:        p.str("cwd"),
		Model:      p.str("model"),
		Transcript: p.str("transcript_path"),
	}
	switch p.str("hook_event_name") {
	case "SessionStart":
		ev.Kind = KindSessionStart
	case "UserPromptSubmit":
		ev.Kind = KindPrompt
	case "PostToolUse", "PostToolUseFailure":
		// A command that fails fires the second of these, not the first; the
		// command may still have moved HEAD (git commit && go test).
		ev.Kind = KindTool
		ev.Tool = p.str("tool_name")
		ev.Command = p.str("tool_input.command")
		ev.Description = p.str("tool_input.description")
		ev.Files = p.editedFiles("tool_input")
		ev.Exit = p.exitStatus("tool_response")
	case "Stop":
		ev.Kind = KindStop
		ev.StopActive = p.flag("stop_hook_active")
	case "SessionEnd":
		ev.Kind = KindSessionEnd
	default:
		return Event{}, nil
	}
	return ev, nil
}

// claudeEnvFile is the variable that names, in the environment of a
// SessionStart hook, the file Claude Code sources before each Bash command of
// the session.
const claudeEnvFile = "CLAUDE_ENV_FILE"

// SetEnv appends an export line per variable to the file CLAUDE_ENV_FILE
// names, when that is the file Claude Code made for this session: one in a
// directory named after the session id. CLAUDE_ENV_FILE may instead be a
// script of the person's own, which Claude Code sources before each Bash
// command and a hook inherits (a hook run under PowerShell, a harness that
// is not Claude Code); jig never writes to that, and sets nothing.
func (a claudeShape) SetEnv(ev Event, getenv func(string) string, vars map[string]string) (bool, error) {
	path := getenv(claudeEnvFile)
	if !a.envFile || path == "" || ev.Session == "" || filepath.Base(filepath.Dir(path)) != ev.Session {
		return false, nil
	}
	return appendExports(path, vars)
}

// claudeHookEvents names, by kind of Event, the hook events whose
// hookSpecificOutput carries additionalContext.
var claudeHookEvents = map[string]string{
	KindSessionStart: "SessionStart",
	KindPrompt:       "UserPromptSubmit",
	KindTool:         "PostToolUse",
}

func (a claudeShape) Render(ev Event, r Result) []byte {
	return renderHookOutput(ev, r, claudeHookEvents, "block")
}

// hookOutput is the JSON a hook prints in the shape Claude Code reads, and
// the harnesses that follow it.
type hookOutput struct {
	Decision      string           `json:"decision,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	SystemMessage string           `json:"systemMessage,omitempty"`
	Specific      *hookSpecificOut `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOut struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// renderHookOutput is Render for the harnesses that read that JSON: events
// names the kinds that take context, refuse is the decision that keeps the
// agent working. Context for a kind with no channel for it goes to the
// person, so nothing is dropped.
func renderHookOutput(ev Event, r Result, events map[string]string, refuse string) []byte {
	var out hookOutput
	message := r.Message
	if name, ok := events[ev.Kind]; ok {
		if r.Context != "" {
			out.Specific = &hookSpecificOut{HookEventName: name, AdditionalContext: r.Context}
		}
	} else {
		message = joinText(r.Context, message)
	}
	out.SystemMessage = message
	if ev.Kind == KindStop && r.Block != "" {
		out.Decision, out.Reason = refuse, r.Block
	}
	if out == (hookOutput{}) {
		return nil
	}
	return jsonLine(out)
}
