package observe

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// noteCtx is what the note sources are given for this event.
func (h *handler) noteCtx(mem sessionMemory, commit string) NoteCtx {
	return NoteCtx{
		Event:      h.ev,
		Where:      h.w,
		Store:      h.st,
		JigHome:    h.d.JigHome,
		Now:        h.d.Now(),
		Since:      mem.started,
		Commit:     commit,
		Unattended: h.unattended(),
		Journal:    h.journal,
	}
}

// collect asks every note source for its notes. A source that fails costs its
// own notes, never the others'. The notes come in source order, each Ref
// once. Without a ticket in a store that is here there is nothing for a
// source to look at.
func (h *handler) collect(mem sessionMemory, commit string) []Note {
	if h.st == nil || h.w.Ticket == "" {
		return nil
	}
	c := h.noteCtx(mem, commit)
	var out []Note
	seen := map[string]bool{}
	for _, src := range h.d.Sources {
		notes, err := callSource(src, c)
		if err != nil {
			h.fail(fmt.Errorf("note source %s: %w", src.Name, err))
			continue
		}
		for _, n := range notes {
			if n.Ref == "" || n.Text == "" || seen[n.Ref] {
				continue
			}
			n.Source = src.Name
			seen[n.Ref] = true
			out = append(out, n)
		}
	}
	return out
}

// callSource calls a source, turning its panic into its error.
func callSource(src NamedSource, c NoteCtx) (notes []Note, err error) {
	defer func() {
		if r := recover(); r != nil {
			notes, err = nil, fmt.Errorf("panic: %v", r)
		}
	}()
	return src.Fn(c)
}

// deliver gives the session the notes it has not heard, as the context text
// of the event, and records that it did (a note-delivered line each). A
// session with no id cannot be told apart from another, so it hears none.
func (h *handler) deliver(outcome, commit string) string {
	if h.ev.Session == "" {
		return ""
	}
	mem := h.memory()
	var fresh []Note
	for _, n := range h.collect(mem, commit) {
		if !mem.delivered[n.Ref] {
			fresh = append(fresh, n)
		}
	}
	if len(fresh) == 0 {
		return ""
	}
	for _, n := range fresh {
		h.delivered(n.Ref, n.Text, outcome)
	}
	b := axi.Block{Header: fmt.Sprintf("notes[%d]:", len(fresh))}
	for _, n := range fresh {
		b.Lines = append(b.Lines, "  "+n.Text)
	}
	return b.String()
}

// delivered records that the session was told ref.
func (h *handler) delivered(ref, text, outcome string) {
	l := h.base(journal.EventNoteDelivered)
	l.Ref = ref
	l.Outcome = outcome
	l.Summary = oneLine(text, 600)
	h.fail(ledger.Append(h.d.JigHome, l))
}

// bullets renders findings as the lines of a message, one each.
func bullets(texts []string) string {
	return "- " + strings.Join(texts, "\n- ")
}
