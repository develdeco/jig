package tui

import (
	"fmt"
	"strconv"

	"github.com/develdeco/jig/internal/journal"
)

// fixRow is a fix slice a round sent back: its id and its row.
type fixRow struct {
	id, line string
}

// fixRows are the fix slices the rounds sent back, oldest first: the state
// each is in, the round, and the finding it fixes.
func (m *Model) fixRows(t *ticket) []fixRow {
	states := map[string]string{}
	for _, s := range t.slices(m.zone) {
		states[s.ID] = s.State
	}
	var out []fixRow
	seen := map[string]bool{}
	for _, l := range t.lines {
		if l.Event != journal.EventFixSlice || l.Slice == "" || seen[l.Slice] {
			continue
		}
		seen[l.Slice] = true
		state := firstOf(states[l.Slice], StateQueued)
		line := m.st.icon(state) + " " + l.Slice + m.st.dim("  round "+strconv.Itoa(max(1, l.Attempt))+"  ") + t.goals()[l.Slice]
		out = append(out, fixRow{id: l.Slice, line: line})
	}
	return out
}

// reviewPage is a ticket's review: each gate round and who reviewed it, the
// asks a round left for a person and what has been decided about each, the
// reviewing sessions, and the review's activity.
func (m *Model) reviewPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepReview]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))

	p.add("")
	p.add(m.st.bold("rounds"))
	rounds := t.rounds(m.zone)
	if len(rounds) == 0 {
		p.add(m.st.dim("no round yet: a fresh session reviews the change once every slice is green"))
	}
	for _, r := range rounds {
		word := map[string]string{StateReviewing: "open", StateFindings: "findings", StateReady: "clean"}[r.State]
		state := r.State
		if state == StateReady {
			state = StateOK
		}
		line := m.st.icon(state) + " round " + fmt.Sprint(r.N) + "  " + m.st.state(state, pad(word, 8))
		switch {
		case r.State == StateReviewing:
			line += m.st.dim("  opened " + r.At + ", waiting for the reviewer to finish")
		case r.Reviewer != "":
			line += m.st.dim("  by " + m.whoWith(t, r.Reviewer) + " at " + r.At)
		default:
			line += m.st.dim("  " + r.At)
		}
		p.add(line)
	}

	if fixes := m.fixRows(t); len(fixes) > 0 {
		p.add("")
		p.add(m.st.bold("sent back to the build"))
		for _, f := range fixes {
			p.addRow(rowRef{kind: rowSlice, id: f.id, ticket: id, slice: f.id}, f.line)
		}
	}

	p.add("")
	p.add(m.st.bold("asks"))
	asks := t.asks()
	switch {
	case t.status == nil:
		p.add(m.st.dim("reading the ticket's status"))
	case len(asks) == 0:
		p.add(m.st.dim("nothing: no finding waits for a person"))
	}
	for i, line := range m.askLines(asks) {
		p.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		if why := asks[i].Why; why != "" && why != "-" {
			p.add("    " + m.st.dim(why))
		}
	}

	if rows := m.sessionRows(t, roleReviewer); len(rows) > 0 {
		p.add("")
		p.add(m.st.bold("sessions"))
		for _, r := range rows {
			p.add(r)
		}
	}

	p.add("")
	m.activityPanel(&p, t, stepReview)
	return p
}
