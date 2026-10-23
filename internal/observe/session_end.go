package observe

import (
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// sessionEnd closes the session and leaves a note of a question it left open.
func (h *handler) sessionEnd() Result {
	if !h.locate() {
		return Result{}
	}
	h.hook("")
	if h.ev.Session != "" {
		h.fail(ledger.EndSession(h.d.JigHome, h.d.Now(), h.ev.Session))
	}
	if h.st == nil || h.w.Ticket == "" {
		return Result{}
	}
	// The sources get their call: it is when one that keeps a running total
	// writes its last line. What they return is not delivered.
	h.collect(h.memory(), "")
	h.noteOpenQuestions()
	return Result{}
}

// noteOpenQuestions writes a note line in the ticket journal for each question
// still open when the session ends, once per session and question, so
// whoever picks the ticket up next knows the session left without an answer.
// The line stays in the store clone until the next checkpoint pushes it.
func (h *handler) noteOpenQuestions() {
	questions, err := h.st.ReadQuestions(h.w.Ticket)
	if err != nil {
		h.fail(err)
		return
	}
	lines, err := h.journal()
	if err != nil {
		h.fail(err)
		return
	}
	for _, q := range questions {
		if q.Status != "open" || h.noted(lines, q.ID) {
			continue
		}
		h.fail(journal.Append(h.st, h.w.Ticket, journal.Line{
			TS:         ledger.Stamp(h.d.Now()),
			Slice:      q.Slice,
			Event:      journal.EventNote,
			Session:    h.ev.Session,
			Agent:      h.ev.Agent,
			Harness:    h.ev.Harness,
			Ref:        q.ID,
			Provenance: "jig",
			Summary:    oneLine("the session ended with question "+q.ID+" still open: "+firstLine(q.Body), 600),
		}))
	}
}

// noted reports whether the journal already holds this session note of the
// open question.
func (h *handler) noted(lines []journal.Line, qid string) bool {
	for _, l := range lines {
		if l.Event == journal.EventNote && l.Ref == qid && l.Session == h.ev.Session {
			return true
		}
	}
	return false
}
