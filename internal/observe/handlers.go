package observe

import (
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
)

// sessionStart sets JIG_SESSION for the session's shell commands, records
// the session and gives it the context of `jig here`. The variable is set
// wherever the session starts, since it may work in a project later; where
// the harness cannot set it, a session in a project is asked to. The line
// that records the session carries the transcript path the harness reported,
// so a later reader can find what the session was asked (ledger.Session).
func (h *handler) sessionStart() Result {
	set := h.handOverSession()
	if !h.locate() {
		return Result{}
	}
	if h.ev.Session != "" {
		h.fail(ledger.StartSession(h.d.JigHome, h.d.Now(), ledger.Registration{
			ID:         h.ev.Session,
			Harness:    h.ev.Harness,
			Agent:      h.ev.Agent,
			Cwd:        h.w.Cwd,
			Clone:      h.w.RepoDir,
			Ticket:     h.w.Ticket,
			Slice:      h.w.Slice,
			Model:      h.ev.Model,
			Transcript: h.transcript(),
			Commit:     h.head(),
		}))
	}
	h.hook("")
	var res Result
	if h.d.Here != nil {
		text, err := h.d.Here(h.w, h.st)
		h.fail(err)
		res.Context = strings.TrimSpace(text)
	}
	if !set && h.ev.Session != "" {
		res.Context = joinText(res.Context, sessionAsk(h.ev.Session))
	}
	return res
}

// handOverSession sets JIG_SESSION to the event's session for the session's
// later shell commands, and reports whether the harness did.
func (h *handler) handOverSession() bool {
	if h.ev.Session == "" || h.d.SetEnv == nil {
		return false
	}
	set, err := h.d.SetEnv(map[string]string{locate.SessionEnv: h.ev.Session})
	h.fail(err)
	return set && err == nil
}

// sessionAsk asks the agent to pass its session to the jig commands it runs,
// for a harness that could not set JIG_SESSION for them.
func sessionAsk(id string) string {
	return "jig knows this session as " + id + ", and the harness could not tell your shell commands: run each jig command with " +
		locate.SessionEnv + "=" + id + " in its environment, or with --session " + id + " on the commands that take it (the session argument of a jig MCP tool)."
}

// prompt gives the session the notes it has not heard, and records a move to
// another ticket or slice.
func (h *handler) prompt() Result {
	if !h.locate() {
		return Result{}
	}
	mem := h.memory()
	h.hook("")
	h.noteMove(mem)
	return Result{Context: h.deliver(outcomePrompt, "")}
}

// noteMove records a session-touch line when the session works on a ticket or
// a slice its earlier lines did not name: a move from one to another, and
// the first ticket of a session that started on none. The line carries the
// harness and model the session started with, so a reader of the ticket's
// stream, which leaves out lines about no ticket, still learns who the
// session is, and the transcript path of this event: a session whose start
// jig did not see (the hooks came after it, or it began outside a project)
// has no other line that gives one.
func (h *handler) noteMove(mem sessionMemory) {
	if h.ev.Session == "" || h.w.Ticket == "" || (h.w.Ticket == mem.ticket && h.w.Slice == mem.slice) {
		return
	}
	h.fail(ledger.TouchSession(h.d.JigHome, h.d.Now(), ledger.Registration{
		ID: h.ev.Session, Harness: firstNonEmpty(h.ev.Harness, mem.harness), Agent: h.ev.Agent, Model: firstNonEmpty(h.ev.Model, mem.model),
		Cwd: h.w.Cwd, Clone: h.w.RepoDir, Ticket: h.w.Ticket, Slice: h.w.Slice, Transcript: h.transcript(),
	}))
}

// transcript is the transcript path the event reports for the session itself:
// none for an event from a subagent, whose hook may name a transcript of its
// own, which says nothing of what the session was asked.
func (h *handler) transcript() string {
	if h.ev.Agent != "" {
		return ""
	}
	return h.ev.Transcript
}

// firstNonEmpty is the first of ss that is not empty.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// tool records the tool call, verifies a commit it made, and gives the
// session the notes it has not heard.
func (h *handler) tool() Result {
	if !h.locate() {
		return Result{}
	}
	mem := h.memory()
	h.noteMove(mem)

	// A tool that ran a command may have moved HEAD. The session last line
	// that recorded HEAD says where it was; with none, this call is where
	// HEAD starts being watched.
	head, moved := "", ""
	if h.ev.Command != "" {
		head = h.head()
		if head != "" && mem.head != "" && head != mem.head && sameDir(mem.headCwd, h.w.Cwd) {
			moved = head
		}
	}
	if moved != "" {
		h.verifyCommit(moved)
	}

	l := h.base(journal.EventTool)
	l.Tool = h.ev.Tool
	l.Command = oneLine(h.ev.Command, 400)
	l.Description = oneLine(h.ev.Description, 200)
	l.Exit = h.ev.Exit
	l.Files = h.ev.Files
	l.Commit = head
	l.Branch = h.w.Branch
	h.fail(ledger.Append(h.d.JigHome, l))

	return Result{Context: h.deliver(outcomeTool, moved)}
}

// sameDir reports whether two directories recorded by different lines are
// one.
func sameDir(a, b string) bool {
	return a == b || gitx.SameDir(a, b)
}

// firstLine is s up to its first line break.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
