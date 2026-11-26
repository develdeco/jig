package tui

import (
	"slices"

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

// subject is what the keys of a screen act on: the ticket (the open one, else
// the one under the cursor on main and a chart) and the slice (the slice
// screen's, else the one the cursor's row is about), "" for none.
type subject struct {
	ticket, slice string
	// onSlice says the slice is the screen's own.
	onSlice bool
	// row is the row under the cursor, when hasRow: what enter would open. It
	// is not part of what the page is sized by (see same).
	row    rowRef
	hasRow bool
	// panes are the titles of the screen's panes in drawing order and focus the
	// one that has the focus: the hint line names them, so the page is sized by
	// them too.
	panes []string
	focus string
	// zoom is the pane drawn over the whole body, "" for none, and search says a
	// pane is narrowed by a query.
	zoom   string
	search bool
	// cut says the row under the cursor holds a cut mark, and expanded that its
	// holder is laid out whole: what `space` expands or folds. Like the row they
	// are not part of what the page is sized by.
	cut, expanded bool
	// textPane says the focused pane has no cursor rows and is the holder.
	textPane bool
	// pane is the title of the pane the row under the cursor is in.
	pane string
	// lastCut and lastExpanded say the same of the last command line, which the
	// screen holds: `space` reaches it when the focus holds nothing cut.
	lastCut, lastExpanded bool
}

// same says whether o is the subject s is, whatever row the cursor is on: the
// page is sized by the keys of a subject, not by the row.
func (s subject) same(o subject) bool {
	return s.ticket == o.ticket && s.slice == o.slice && s.onSlice == o.onSlice &&
		s.focus == o.focus && s.zoom == o.zoom && s.search == o.search && slices.Equal(s.panes, o.panes)
}

// paneSide is the title of the pane delta places from the focused one, wrapping.
func (s subject) paneSide(delta int) string {
	n := len(s.panes)
	if n == 0 {
		return ""
	}
	i := max(0, slices.Index(s.panes, s.focus))
	return s.panes[((i+delta)%n+n)%n]
}

// paneWords is what a key that goes to the pane delta places on says: its words and
// the pane's name, or the words alone on a screen with no other pane to go to
// (the key's row gives the reason).
func (s subject) paneWords(words string, delta int) string {
	if len(s.panes) < 2 {
		return words
	}
	return words + ": " + s.paneSide(delta)
}

// canSwitchPane is the availability of the pane keys: a screen of one pane has
// none to move to.
func canSwitchPane(_ *Model, sub subject) (bool, string) {
	if len(sub.panes) < 2 {
		return false, "this screen has one pane"
	}
	return true, ""
}

// subjectNow reads the subject from the page on top.
func (m *Model) subjectNow() subject {
	m.followSel()
	return m.subjectOf(m.page())
}

// subjectOf is the subject of the screen on top whose page is pg.
func (m *Model) subjectOf(pg page) subject {
	e := m.top()
	ref := m.refIn(pg)
	var s subject
	if e.screen != screenMain && e.screen != screenChart {
		s.ticket = e.ticket
	} else if ref != nil && ref.kind == rowTicket {
		s.ticket = ref.id
	}
	switch {
	case e.screen == screenSlice:
		s.slice, s.onSlice = e.slice, true
	case ref != nil && (ref.kind == rowSlice || ref.kind == rowQuestion) && ref.ticket == s.ticket:
		s.slice = ref.slice
	}
	if ref != nil {
		s.row, s.hasRow = *ref, true
		k, _ := pg.holderOfSel(e.sel)
		if k.pane == "" {
			k = expKey{pane: e.paneFocus, id: ref.identity()}
		}
		s.cut = pg.holdsCut(k)
		s.pane = k.pane
		s.expanded = e.isExpanded(s.pane, ref)
	} else if e.textFocus && e.paneFocus != "" {
		// A pane with no cursor rows is its own holder.
		s.textPane = true
		s.cut = pg.holdsCut(expKey{pane: e.paneFocus})
		s.expanded = e.paneOpen(e.paneFocus)
	}
	s.panes, s.focus, s.zoom, s.search = m.panesNow, m.focusedPane(), e.zoom, e.psearch.applied()
	s.lastCut, s.lastExpanded = m.lastState()
	return s
}

// canOn says whether action a can run now on what the keys act on (sub), and
// when it cannot, why in a short sentence. It reads the roster's digest and the
// status already read and runs nothing: the hints list a key by it and the key
// handler runs a verb by it, so the two cannot disagree. The digest is the same
// reading `jig next` gives, so no phase is derived here.
func (m *Model) canOn(sub subject, a action) (bool, string) {
	id := sub.ticket
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
	ok, why := m.canRun(t, sub, a)
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
func (m *Model) canRun(t *ticket, sub subject, a action) (bool, string) {
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
		if sub.onSlice {
			if len(t.openQuestions(m.zone, sub.slice)) > 0 {
				return true, ""
			}
			return false, "no question is waiting for an answer"
		}
		if len(t.openQuestions(m.zone, "")) > 0 {
			return true, ""
		}
		if d.waitsForAnswer() && len(t.questions(m.zone)) == 0 {
			// The roster counts a question the stream has not brought yet.
			return false, readingText
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
		if sub.slice != "" {
			if t.status == nil {
				return false, readingText
			}
			if slices.Contains(t.stuckSlices(), sub.slice) {
				return true, ""
			}
			return false, "slice " + sub.slice + " is not stalled or env-blocked"
		}
		// The verb acts on the slices the status names, so the status decides.
		if t.status == nil {
			return false, readingText
		}
		if len(t.stuckSlices()) > 0 {
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

// stuckSlices are the slices the status says are stalled or env-blocked.
func (t *ticket) stuckSlices() []string {
	if t == nil || t.status == nil {
		return nil
	}
	var out []string
	for _, s := range t.status.Slices {
		if s.State == StateStalled || s.State == StateEnvBlocked {
			out = append(out, s.ID)
		}
	}
	return out
}
