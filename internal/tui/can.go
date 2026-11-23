package tui

import (
	"github.com/develdeco/jig/internal/phase"
)

// action is something a key does to the ticket it acts on, by the name the
// availability question is asked in.
type action string

const (
	actAnswer  action = "answer"
	actKeep    action = "keep"
	actDismiss action = "dismiss"
	actGate    action = "gate"
	actPublish action = "publish"
	actRequeue action = "requeue"
	actApprove action = "approve"
	// actLandWord is not a key: it is the person's word to the session at a
	// landing stop, listed where the merge keys would be.
	actLandWord action = "land-word"
)

// can says whether action a can run now on the ticket a key acts on (the open
// one, else the one under the cursor, as targetTicket says), and when it cannot,
// why in a short sentence. It reads the roster's digest and the status already
// read and runs nothing: the hints list a key by it and the key handler runs a
// verb by it, so the two cannot disagree. The digest is the same reading `jig
// next` gives, so no phase is derived here.
func (m *Model) can(a action) (bool, string) {
	return m.canOn(m.targetTicket(), a)
}

// canOn is can for the ticket id.
func (m *Model) canOn(id string, a action) (bool, string) {
	t := m.w.tickets[id]
	switch {
	case id == "" || t == nil:
		return false, "select a ticket first"
	case t.awaiting:
		return false, id + " does not exist yet"
	case t.digest == nil:
		return false, readingText
	}
	if a == actLandWord {
		return m.landingNeedsYou(t), ""
	}
	ok, why := m.canRun(t, a)
	if !ok && m.landingNeedsYou(t) {
		// At the landing stop nothing runs from here: the session merges.
		why = landingWord(id)
	}
	return ok, why
}

// landingWord is why a key does nothing at a landing stop.
func landingWord(id string) string {
	return `landing is said to the session: "Land ` + id + `"`
}

// canRun is can for a ticket the roster has read.
func (m *Model) canRun(t *ticket, a action) (bool, string) {
	d := t.digest
	switch a {
	case actGate:
		if d.Current == string(phase.Review) && d.Ready {
			return true, ""
		}
		return false, notReady(t, phase.Review)
	case actPublish:
		if d.Current == string(phase.Publish) && d.Ready && !d.Behind {
			return true, ""
		}
		return false, notReady(t, phase.Publish)
	case actAnswer:
		if d.waitsForAnswer() || len(t.openQuestions(m.zone, "")) > 0 {
			return true, ""
		}
		return false, "no question is waiting for an answer"
	case actKeep, actDismiss:
		if t.status == nil {
			return false, readingText
		}
		if len(t.asks()) > 0 {
			return true, ""
		}
		return false, "no ask is waiting for a decision"
	case actRequeue:
		if d.Slices != nil && d.Slices.Stalled+d.Slices.EnvBlocked > 0 {
			return true, ""
		}
		return false, "no slice is stalled or env-blocked"
	case actApprove:
		if p, _ := d.phase(phase.Brief); p.NeedsYou {
			return true, ""
		}
		return false, "the brief is not waiting for your approval"
	}
	return false, string(a) + " is not an action"
}

// notReady is why phase p cannot run now, in the words the digest gives the
// phase.
func notReady(t *ticket, p phase.Name) string {
	d := t.digest
	if d.Closed != "" {
		return t.id + " is closed"
	}
	row, ok := d.phase(p)
	if !ok {
		return t.id + " has no " + string(p) + " step"
	}
	if row.State == phase.StateDone {
		return "the " + string(p) + " is done: " + row.Text
	}
	return "the " + string(p) + " is not ready: " + row.Text
}
