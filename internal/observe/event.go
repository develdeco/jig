package observe

import (
	"fmt"
	"strings"
)

// The kinds of Event, the moments of an agent session jig acts on.
const (
	// KindSessionStart: a session began, resumed or was cleared or compacted.
	KindSessionStart = "session-start"
	// KindPrompt: the person sent a prompt, before the agent works on it.
	KindPrompt = "prompt"
	// KindTool: a tool call finished.
	KindTool = "tool"
	// KindStop: the agent is about to end its turn.
	KindStop = "stop"
	// KindSessionEnd: the session ended.
	KindSessionEnd = "session-end"
	// KindPostCommit: git made a commit. It comes from git's post-commit hook,
	// not from a harness, so it has no session of its own.
	KindPostCommit = "post-commit"
)

// Kinds lists the kinds of Event.
func Kinds() []string {
	return []string{KindSessionStart, KindPrompt, KindTool, KindStop, KindSessionEnd, KindPostCommit}
}

// ValidKind reports whether k is a kind of Event.
func ValidKind(k string) bool {
	for _, v := range Kinds() {
		if k == v {
			return true
		}
	}
	return false
}

// Event is what one hook call says, in jig's own words. A harness adapter
// builds it from the harness's payload; the core reads only this, never a
// harness's own shape. Every field but Kind may be empty: an adapter fills
// what its harness reports.
type Event struct {
	// Harness names the harness that sent the event ("claude", "codex", ...;
	// "git" for a post-commit, "neutral" for one a script built).
	Harness string
	// Kind is one of the Kind constants.
	Kind string
	// Session is the harness's session id, Agent the id of the subagent that
	// is speaking ("" for the session's own agent).
	Session string
	Agent   string
	// Cwd is the directory the event happened in.
	Cwd string
	// Tool is the name of the tool a tool event is about, Command the shell
	// command it ran when it ran one, Description what the session said the
	// call is for, and Exit that command's exit status when the harness
	// reports it.
	Tool        string
	Command     string
	Description string
	Exit        *int
	// Files are the files an editing tool touched.
	Files []string
	// Model is the model the session runs, Transcript the path of the
	// harness's transcript of the session.
	Model      string
	Transcript string
	// StopActive is true on a stop event that follows an earlier refusal to
	// stop: the agent is already continuing because a hook asked it to.
	StopActive bool
}

// Result is what the core hands back for an Event, in jig's own words. An
// adapter renders it into the shape its harness reads.
type Result struct {
	// Context is text for the agent to read: the harness adds it to what the
	// model sees.
	Context string
	// Message is text for the person, shown by the harness but not given to
	// the model.
	Message string
	// Block, when not empty, is the reason the agent should not stop yet; the
	// harness feeds it to the agent and the agent continues.
	Block string
}

// Empty reports whether r says nothing.
func (r Result) Empty() bool { return r.Context == "" && r.Message == "" && r.Block == "" }

// Validate reports why e cannot be acted on, or nil.
func (e Event) Validate() error {
	if !ValidKind(e.Kind) {
		return fmt.Errorf("observe: %q is not a kind of event (they are %s)", e.Kind, strings.Join(Kinds(), ", "))
	}
	return nil
}
