package tui

import (
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// Where a ticket stands is not worked out here: internal/phase derives it from
// the facts of the ticket and its journal lines, and `jig next`, `jig status`
// and the claim and gate refusals read the same derivation. The screens gather
// the facts from what they know (the status the verbs printed, the lines of the
// stream), so a ticket cannot be at one step on a screen and at another in the
// words an agent is given.

// phaseOf is where the ticket stands: phase.Derive over the facts the screens
// hold of it.
func (m *Model) phaseOf(t *ticket) phase.Ticket {
	f, lines := m.factsOf(t)
	return phase.Derive(f, lines)
}

// factsOf gathers what internal/phase derives from, and the journal lines it
// reads beside them. The status is the store's word on the brief, the slices
// and the asks; with none read yet (the main screen reads none) the journal and
// the roster are all there is, and the answer is what they show. Two facts are
// not in the status: the gate rounds' reports, so the journal's gate lines decide
// a round's verdict, and the blockers, so a ticket held up by another one's
// publish reads as ready here and as blocked in `jig next`.
func (m *Model) factsOf(t *ticket) (phase.Facts, []journal.Line) {
	lines := make([]journal.Line, len(t.lines))
	for i, l := range t.lines {
		lines[i] = l.Line
	}
	f := phase.Facts{
		Ticket:      t.id,
		Open:        phase.OpenRoundIn(lines),
		Published:   t.publish().Done,
		SessionName: func(session string) string { return m.whoWith(t, session) },
		ReviewerName: func(session string, round int, detailed bool) string {
			if detailed {
				return m.reviewerWho(t, session, round, "", nil)
			}
			return m.reviewerRole(t, session)
		},
	}
	fixes := t.fixRounds()
	if st := t.status; st != nil {
		f.Adopted, f.Branch = st.Branch != "", st.Branch
		f.Closed = st.Closed
		if b := st.Brief; b != nil {
			f.BriefExists, f.Sections = true, len(b.Sections)
			f.Approval, f.ApprovedBy, f.ApprovedHow = b.Approval, b.ApprovedBy, b.Provenance
		}
		for _, s := range st.Slices {
			f.Slices = append(f.Slices, phase.Slice{
				ID: s.ID, State: s.State, Goal: s.Goal, FromGate: fixes[s.ID],
				BlockedBy:    splitPlus(s.BlockedBy),
				OpenQuestion: len(t.openQuestions(m.zone, s.ID)) > 0,
			})
		}
		for _, a := range st.Asks {
			f.Asks = append(f.Asks, phase.Ask{ID: a.ID})
		}
		return f, lines
	}

	// No status yet: the slices the journal names, in the states it shows, and
	// an approval line as the brief's.
	for _, id := range t.sliceIDsFromLines() {
		f.Slices = append(f.Slices, phase.Slice{
			ID: id, State: t.guessSliceState(id), FromGate: fixes[id],
			OpenQuestion: len(t.openQuestions(m.zone, id)) > 0,
		})
	}
	if i := t.latest("", journal.EventApproval); i >= 0 {
		f.BriefExists, f.Approval = true, phase.ApprovalCurrent
		f.ApprovedBy, f.ApprovedHow = personName(t.lines[i].Summary), t.lines[i].Provenance
	}
	// A review with no slice ever built is a branch the ticket took over.
	if len(f.Slices) == 0 && len(t.stepLines(stepReview)) > 0 {
		f.Adopted = true
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

// fixRounds maps each fix slice to the round whose findings queued it: the
// fix-slice line's attempt, else the round that had closed by then.
func (t *ticket) fixRounds() map[string]int {
	out := map[string]int{}
	closed := 0
	for _, l := range t.lines {
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
				n = max(closed, 1)
			}
			out[l.Slice] = n
		}
	}
	return out
}

// Standing is where the screens put a ticket among its four phases: the first
// that is not done, whether it can start, and each phase's state.
type Standing struct {
	// Current is brief, build, review or publish: the phase the Steps panel
	// marks current (the last when the ticket is published).
	Current string
	// Ready is whether Current can start now.
	Ready bool
	// States are the four phases' states in order: done, current or pending.
	States [4]string
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
	steps := m.stepRows(t, d)
	cur := currentStep(steps)
	s := Standing{Current: strings.ToLower(stepNames[cur]), Ready: d.Ready}
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

// StandingOf is the Standing of a ticket the screens would hold after reading
// the output of `jig status <ticket>` and the ticket's journal lines: the whole
// path from the verb's text to the Steps panel, with no terminal and no store.
func StandingOf(ticket, status string, lines []events.Envelope) Standing {
	m := New(Config{Zone: time.UTC})
	m.w.apply(lines)
	t, _ := m.w.ticketFor(ticket)
	if status != "" {
		t.status = parseStatus(status)
		if t.status.Err != nil {
			t.status = nil
		}
	}
	s, _ := m.Standing(ticket)
	return s
}
