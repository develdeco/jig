package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// An unattended session is the one to take its ticket to the next phase:
// nobody is there to do it for the session, and stopping would leave built work
// waiting for a person to remember it. This source tells the session so at
// Stop, and the observe core holds an unattended session back once for each
// note (an interactive one is only told, and is never held: the person is
// there). The notes are the two moments a ticket is left between phases:
//
//   - every slice green and no review round open: open the gate and review;
//   - a clean round, and a person approved publishing its head: publish.
func init() {
	observe.RegisterNoteSource("next-phase", nextPhaseNotes)
}

// nextPhaseNotes is the note source. It speaks at a Stop only, where a session
// is about to leave the ticket. The publish note comes first, to any session;
// the review note only to an unattended one.
func nextPhaseNotes(c observe.NoteCtx) ([]observe.Note, error) {
	if c.Event.Kind != observe.KindStop || c.Store == nil || c.Where.Ticket == "" {
		return nil, nil
	}
	if c.Event.IsSubagent() {
		// The main session publishes (a build subagent never does), so a
		// subagent's stop hears the review note alone, when it holds an
		// unattended claim: c.Unattended is the claim of this agent.
		if !c.Unattended {
			return nil, nil
		}
		return reviewNotes(c)
	}
	notes, err := publishNotes(c)
	if err != nil || len(notes) > 0 || !c.Unattended {
		return notes, err
	}
	return reviewNotes(c)
}

// reviewNotes is the note for a ticket whose build is done and whose review
// has not begun, or whose last round found things that have been fixed since:
// the phase the ticket is at is review, it can start (no round is open, no ask
// waits for a decision), and the session is told to open the gate. It names
// who reviews, from the freshness of the session that is about to stop
// (verifydeliver.Freshness): that session built the ticket more often than not,
// and then a subagent reviews. The Ref names the round to come, so a note is
// given once for each round.
func reviewNotes(c observe.NoteCtx) ([]observe.Note, error) {
	t, err := derivePhase(c.Store, c.Where.Ticket)
	if err != nil {
		return nil, err
	}
	if t.Current != phase.Review || !t.Ready {
		return nil, nil
	}
	f, err := verifydeliver.Freshness(verifydeliver.Deps{Store: c.Store, Home: c.JigHome}, c.Where.Ticket, c.Event.Session)
	if err != nil {
		f = verifydeliver.Fresh{}
	}
	return []observe.Note{{
		Ref:     fmt.Sprintf("review:%s:%d", c.Where.Ticket, closedRounds(t)+1),
		Text:    reviewNoteText(f, c.Where.Ticket),
		Enforce: true,
	}}, nil
}

// publishNotes is the note for a ticket that is reviewed clean and that a
// person approved to publish. It reads the store's word on the ticket's gate
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
