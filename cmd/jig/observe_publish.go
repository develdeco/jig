package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// A ticket that is reviewed clean and that a person approved to publish has
// one step left, and an unattended session is the one to take it: nobody is
// there to do it for the session, and stopping would leave an approved ticket
// waiting for a person to remember it. This source tells the session so at
// Stop, and the observe core holds an unattended session back once for the
// approval (an interactive one is only told: the person is there).
func init() {
	observe.RegisterNoteSource("publish", publishNotes)
}

// publishNotes is the note source. It speaks at a Stop only, where a session
// is about to leave the ticket, and reads the store's word on the ticket's gate
// and publish (verifydeliver.ReadStatus, the same reading `jig here` shows):
// the latest round is clean, a person's approval is of the very head it
// reviewed, and the publish is not done. An approval by an agent is not one
// the publish takes for the person's (a bare publish asks a person first), so
// it does not count here either. The Ref names the approved head, so a new
// approval of a new head is a new fact.
func publishNotes(c observe.NoteCtx) ([]observe.Note, error) {
	if c.Event.Kind != observe.KindStop || c.Store == nil || c.Where.Ticket == "" {
		return nil, nil
	}
	s, err := verifydeliver.ReadStatus(c.Store, c.Where.Ticket)
	if err != nil {
		return nil, err
	}
	head, ok := approvedReviewedHead(s)
	if !ok {
		return nil, nil
	}
	short := head
	if len(short) > 12 {
		short = short[:12]
	}
	return []observe.Note{{
		Ref: "publish:" + short,
		Text: fmt.Sprintf("%s is reviewed clean and a person approved publishing %s: run `jig publish %s`, which pushes the reviewed head and opens the pull request",
			c.Where.Ticket, short, c.Where.Ticket),
		Enforce: true,
	}}, nil
}

// approvedReviewedHead is the head a person approved to publish when it is
// the head the latest gate round reviewed and found clean, and nothing of the
// publish is done yet.
func approvedReviewedHead(s verifydeliver.Status) (string, bool) {
	if s.Verdict != "clean" || s.Published || s.ApprovedFor == "" {
		return "", false
	}
	for _, reviewed := range s.ReviewedSHA {
		if reviewed == s.ApprovedFor {
			return reviewed, true
		}
	}
	return "", false
}
