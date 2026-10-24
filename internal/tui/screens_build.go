package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/events"
)

// buildPage is a ticket's build: each slice with its state, its goal, what it
// waits for and the latest thing that happened to it; the sessions that
// worked on the ticket; and the build's activity.
func (m *Model) buildPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepBuild]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))

	slices := t.slices(m.zone)
	if len(slices) == 0 {
		p.add("")
		p.add(m.st.dim("no slices yet"))
		if t.status == nil {
			p.add(m.st.dim("reading the ticket's status"))
		}
		return p
	}
	p.add("")
	p.add(m.st.bold("slices"))
	goals := t.goals()
	idW, stateW := 1, 5
	for _, s := range slices {
		idW = max(idW, width(s.ID))
		stateW = max(stateW, width(s.State))
	}
	for _, s := range slices {
		tries := strconv.Itoa(s.Attempts) + " " + plural(s.Attempts, "try", "tries")
		line := m.st.icon(s.State) + " " + pad(s.ID, idW) + "  " + m.st.state(s.State, pad(s.State, stateW)) +
			"  " + m.st.dim(pad(tries, 7)) + "  " + firstOf(goals[s.ID], m.st.dim("no goal"))
		p.addRow(rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}, line)
		var notes []string
		if s.BlockedBy != "" {
			notes = append(notes, "after slice "+strings.ReplaceAll(s.BlockedBy, "+", " and "))
		}
		if s.Question != "" {
			notes = append(notes, "waits for the answer to "+s.Question)
		}
		if i := t.latest(s.ID, relevantSliceEvents...); i >= 0 {
			notes = append(notes, clock(t.lines[i], m.zone)+" "+m.say(t, t.lines[i]))
		}
		if len(notes) > 0 {
			p.add("    " + spaces(idW) + m.st.dim(strings.Join(notes, " · ")))
		}
	}

	if rows := m.sessionRows(t, roleBuilder); len(rows) > 0 {
		p.add("")
		p.add(m.st.bold("sessions"))
		for _, r := range rows {
			p.add(r)
		}
	}

	p.add("")
	m.activityPanel(&p, t, stepBuild)
	return p
}

// sessionRows are the sessions of the ticket that played role, one row each:
// the name the screens call it by, its harness and model, when it started
// and the start of its id, the one place a session's id is shown.
func (m *Model) sessionRows(t *ticket, role string) []string {
	ids := t.roleSessions()[role]
	nameW := 0
	for _, id := range ids {
		nameW = max(nameW, width(t.roleLabel(id, role)))
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		row := pad(t.roleLabel(id, role), nameW) + "  " + firstOf(m.sessionDesc(id), "harness unknown")
		if start, ok := m.sessionStart(t, id); ok {
			row += m.st.dim("  started " + clock(start, m.zone))
		}
		out = append(out, row+m.st.dim("  "+shortID(id)))
	}
	return out
}

// sessionStart is the ledger's session-start line of a session about the
// ticket, if the screens have seen it.
func (m *Model) sessionStart(t *ticket, session string) (events.Envelope, bool) {
	for _, l := range t.ledger {
		if l.Session == session && l.Event == "session-start" {
			return l, true
		}
	}
	return events.Envelope{}, false
}
