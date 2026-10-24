package tui

import (
	"strings"
	"time"
)

// briefPage is the brief: whether it is approved and by whom, its sections
// and the slices that cite each, the slices it was cut into with their
// goals, and the brief's activity.
func (m *Model) briefPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	if t.status == nil {
		p.add(m.st.dim("reading the ticket's status"))
		return p
	}
	b := t.status.Brief
	step := m.briefStep(t)
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))
	if b != nil && b.ApprovedAt != "" {
		when := b.ApprovedAt
		if at, err := time.Parse(time.RFC3339, b.ApprovedAt); err == nil {
			when = at.In(m.zone).Format("2006-01-02 15:04")
		}
		how := ""
		if b.Provenance == "agent" {
			how = ", recorded by the session that drafted it"
		}
		p.add(m.st.dim("  approved " + when + how))
	}
	if b == nil {
		p.add("")
		p.add(m.st.dim("this ticket has no brief"))
		return p
	}

	p.add("")
	p.add(m.st.bold("sections") + m.st.dim("  cited by"))
	headW := 0
	for _, s := range b.Sections {
		headW = max(headW, width(s.Heading))
	}
	headW = min(headW, max(12, m.width/2))
	for _, s := range b.Sections {
		cited := m.st.dim("no slice")
		if len(s.Slices) > 0 {
			cited = "slice " + strings.Join(s.Slices, ", ")
		}
		p.add(pad(s.Heading, headW) + "  " + cited)
	}

	p.add("")
	p.add(m.st.bold("slices"))
	for _, s := range t.slices(m.zone) {
		line := m.st.icon(s.State) + " " + s.ID + "  " + firstOf(t.goals()[s.ID], m.st.dim("no goal"))
		if s.BlockedBy != "" {
			line += m.st.dim("  after " + strings.ReplaceAll(s.BlockedBy, "+", ", "))
		}
		p.addRow(rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}, line)
	}

	p.add("")
	m.activityPanel(&p, t, stepBrief)
	return p
}
