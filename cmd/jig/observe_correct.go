package main

import (
	"time"

	"github.com/develdeco/jig/internal/correct"
	"github.com/develdeco/jig/internal/observe"
)

// The correction notes: a slice that fails its oracle the same way twice, a
// brief that changed under verified work, a commit outside the workspaces the
// slices build in. internal/correct decides them; this source hands them to the
// observe core, which tells the session at its next tool call or prompt and,
// when the session is unattended, refuses to let it stop once for each.
func init() {
	observe.RegisterNoteSource("correction", correctionNotes)
}

// correctionNotes is the note source. Every correction is enforced: advisory
// to a session with a person at it (the core only tells it), a hold at Stop
// for one without.
func correctionNotes(c observe.NoteCtx) ([]observe.Note, error) {
	switch c.Event.Kind {
	case observe.KindTool, observe.KindPrompt, observe.KindStop:
	default:
		return nil, nil
	}
	if c.Store == nil || c.Where.Ticket == "" {
		return nil, nil
	}
	found, err := correct.Check(correct.Input{
		Store:      c.Store,
		Ticket:     c.Where.Ticket,
		Session:    c.Event.Session,
		Agent:      c.Event.Agent,
		Harness:    c.Event.Harness,
		Slice:      c.Where.Slice,
		Cwd:        c.Where.Cwd,
		Repo:       c.Where.Repo,
		Now:        c.Now,
		Since:      c.Since,
		Commit:     c.Commit,
		Unattended: c.Unattended,
		Journal:    c.Journal,
	})
	notes := make([]observe.Note, len(found))
	for i, n := range found {
		notes[i] = observe.Note{Ref: n.Ref, Text: n.Text, Enforce: true}
	}
	if err != nil && len(notes) > 0 {
		// The core drops the notes of a source that fails. One detector failing
		// (a manifest that does not parse, say) must not cost the session the
		// others, so the failure goes to the log here and the notes go on.
		logObserve(env{now: func() time.Time { return c.Now }}, c.JigHome, "note source correction", err)
		err = nil
	}
	return notes, err
}
