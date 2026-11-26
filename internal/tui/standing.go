package tui

import (
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// Where a ticket stands is not worked out here: internal/phase derives it from
// the facts of the ticket and its journal lines, and `jig next`, `jig status`
// and the claim and gate refusals read the same derivation. The screens gather
// the facts from what they know (the status the verbs printed, the lines of the
// stream), so a ticket cannot be at one step on a screen and at another in the
// words an agent is given.

// phaseOf is where the ticket stands: the words of each phase are the digest's
// (`jig next --json`, the one reading of a ticket every screen shares), and the
// sub-steps and their details are phase.Derive over the status and the journal
// once the status is read. Where the two differ the digest's words win and the
// detail stays. A ticket nothing has been read of stands at "reading".
func (m *Model) phaseOf(t *ticket) phase.Ticket {
	memo := t.memoFor()
	if memo.phase == nil {
		d := readingPhases()
		if t.status != nil {
			t.scanned += len(t.lines)
			f, lines := m.factsOf(t, t.status)
			d = phase.Derive(f, lines)
		}
		if t.digest != nil {
			t.digest.lay(&d)
		}
		addFinalRun(t, &d)
		// A caller that adds a sub-step to a copy adds it to the copy alone.
		for i := range d.Phases {
			d.Phases[i].Subs = slices.Clip(d.Phases[i].Subs)
		}
		memo.phase = &d
	}
	return *memo.phase
}

// subFinal is the key of the build's sub-step for the ticket's last run.
const subFinal = "final"

// addFinalRun gives the build the sub-step of its final run once a round has a
// reviewed head: a run of the union of the slices' checks after the review is
// time the build took and no slice's (finalrun.go).
func addFinalRun(t *ticket, d *phase.Ticket) {
	if runs := t.finalRuns(); runs != nil {
		d.Phases[stepBuild].Subs = append(d.Phases[stepBuild].Subs, finalSub(runs))
	}
}

// factsOf gathers what internal/phase derives the sub-steps from, and the
// journal lines it reads beside them. The status is the store's word on the
// brief, the slices and the asks. Two facts are not in it: the gate rounds'
// reports, so the journal's gate lines decide a round's verdict, and the
// blockers and merges, which only the digest says.
func (m *Model) factsOf(t *ticket, st *statusInfo) (phase.Facts, []journal.Line) {
	lines := make([]journal.Line, len(t.lines))
	for i, l := range t.lines {
		lines[i] = l.Line
	}
	f := phase.Facts{
		Ticket:      t.id,
		Open:        phase.OpenRoundIn(lines),
		Published:   t.publish().Done,
		SessionName: func(session, role string) string { return m.whoWithAs(t, session, role) },
		ReviewerName: func(session string, round int, detailed bool) string {
			if detailed {
				return m.reviewerWho(t, session, round, "", nil)
			}
			return m.reviewerRole(t, session, round)
		},
	}
	f.Adopted, f.Branch = st.Branch != "", st.Branch
	f.Closed = st.Closed
	if b := st.Brief; b != nil {
		f.BriefExists, f.Sections = true, len(b.Sections)
		f.Approval, f.ApprovedBy, f.ApprovedHow = b.Approval, b.ApprovedBy, b.Provenance
	}
	for _, s := range st.Slices {
		f.Slices = append(f.Slices, phase.Slice{
			ID: s.ID, State: s.State, Goal: s.Goal, FromGate: t.fixRound(s.ID), Fix: t.isFixSlice(s.ID) || s.FromGate > 0,
			BlockedBy:    splitPlus(s.BlockedBy),
			OpenQuestion: len(t.openQuestions(m.zone, s.ID)) > 0,
		})
	}
	for _, a := range st.Asks {
		f.Asks = append(f.Asks, phase.Ask{ID: a.ID})
	}
	return f, lines
}

// splitPlus is a status cell of ids joined by "+".
func splitPlus(cell string) []string {
	if cell == "" {
		return nil
	}
	return strings.Split(cell, "+")
}

// fixRounds maps each fix slice the journal names to the round whose findings
// queued it, 0 when nothing places it. A fix-slice line has no attempt as jig
// writes it, so the round is the one closed before the line, or the next one
// when keep-queued lines follow the fix-slice lines: a kept finding of an
// earlier round is queued by the gate that is about to open the next round, and
// the status gives such a slice that round as its from_gate.
func (t *ticket) fixRounds() map[string]int {
	memo := t.memoFor()
	if memo.fixes == nil {
		memo.fixes = t.readFixRounds()
	}
	return memo.fixes
}

// readFixRounds is the reading fixRounds keeps while the lines are the same:
// the map is the memo's, and no caller changes it.
func (t *ticket) readFixRounds() map[string]int {
	out := map[string]int{}
	closed := 0
	for i, l := range t.lines {
		switch l.Event {
		case journal.EventGateRound, journal.EventGateClean:
			if l.Attempt > 0 {
				closed = l.Attempt
			} else {
				closed++
			}
		case journal.EventFixSlice:
			if l.Slice == "" {
				continue
			}
			n := l.Attempt
			if n == 0 {
				n = closed
				if t.keepQueuedFollows(i) {
					n = closed + 1
				}
			}
			out[l.Slice] = n
		}
	}
	return out
}

// keepQueuedFollows is whether the fix-slice line at i belongs to a run of
// fix-slice lines that a keep-queued line closes.
func (t *ticket) keepQueuedFollows(i int) bool {
	for _, l := range t.lines[i+1:] {
		switch l.Event {
		case journal.EventFixSlice:
		case journal.EventKeepQueued:
			return true
		default:
			return false
		}
	}
	return false
}

// isFixSlice is whether the journal names id as a fix slice, placed or not.
func (t *ticket) isFixSlice(id string) bool {
	_, ok := t.fixRounds()[id]
	return ok
}

// fixRound is the round whose findings queued fix slice id: the status's
// from_gate when it lists the slice, else the round the journal places it in,
// 0 when neither does. Every screen that names a fix slice's round asks here.
func (t *ticket) fixRound(id string) int {
	if t.status != nil {
		for _, s := range t.status.Slices {
			if s.ID == id && s.FromGate > 0 {
				return s.FromGate
			}
		}
	}
	return t.fixRounds()[id]
}

// Standing is where the screens put a ticket among its five phases: the first
// that is not done, whether it can start, and each phase's state.
type Standing struct {
	// Current is brief, build, review, publish or land: the phase the ticket
	// stands at, as internal/phase says it (publish once every phase is done).
	Current string
	// Ready is whether Current can start now.
	Ready bool
	// States are the five phases' states in order: done, current or pending.
	States [stepCount]string
}

// Standing is where the screens put ticket, false when they know no such
// ticket. It reads the steps the Steps panel draws, so a test that compares it
// with `jig next` compares what a person sees.
func (m *Model) Standing(ticket string) (Standing, bool) {
	t := m.w.tickets[ticket]
	if t == nil {
		return Standing{}, false
	}
	d := m.phaseOf(t)
	steps, _ := m.rowSteps(t, d)
	cur := currentStep(steps)
	s := Standing{Current: string(d.Current), Ready: d.Ready}
	for i, row := range steps {
		switch {
		case row.Done:
			s.States[i] = phase.StateDone
		case i == cur:
			s.States[i] = phase.StateCurrent
		default:
			s.States[i] = phase.StatePending
		}
	}
	return s, true
}
