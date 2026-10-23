package observe

import (
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// sessionStart records the session and gives it the context of `jig here`.
func (h *handler) sessionStart() Result {
	if !h.locate() {
		return Result{}
	}
	if h.ev.Session != "" {
		h.fail(ledger.StartSession(h.d.JigHome, h.d.Now(), ledger.Registration{
			ID:      h.ev.Session,
			Harness: h.ev.Harness,
			Agent:   h.ev.Agent,
			Cwd:     h.w.Cwd,
			Clone:   h.w.RepoDir,
			Ticket:  h.w.Ticket,
			Slice:   h.w.Slice,
			Model:   h.ev.Model,
			Commit:  h.head(),
		}))
	}
	h.hook("")
	if h.d.Here == nil {
		return Result{}
	}
	text, err := h.d.Here(h.w, h.st)
	h.fail(err)
	return Result{Context: strings.TrimSpace(text)}
}

// prompt gives the session the notes it has not heard, and records a move to
// another ticket or slice.
func (h *handler) prompt() Result {
	if !h.locate() {
		return Result{}
	}
	mem := h.memory()
	h.hook("")
	moved := h.w.Ticket != "" && mem.ticket != "" && (h.w.Ticket != mem.ticket || h.w.Slice != mem.slice)
	if moved && h.ev.Session != "" {
		h.fail(ledger.TouchSession(h.d.JigHome, h.d.Now(), ledger.Registration{
			ID: h.ev.Session, Harness: h.ev.Harness, Cwd: h.w.Cwd, Clone: h.w.RepoDir, Ticket: h.w.Ticket, Slice: h.w.Slice,
		}))
	}
	return Result{Context: h.deliver(outcomePrompt, "")}
}

// tool records the tool call, verifies a commit it made, and gives the
// session the notes it has not heard.
func (h *handler) tool() Result {
	if !h.locate() {
		return Result{}
	}
	mem := h.memory()

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
