package tui

import (
	"strconv"
	"strings"
)

// mainPage is the project: one row per ticket, with its five steps as a row
// of icons and where its current step stands, then a row per chart.
func (m *Model) mainPage() page {
	var p page
	ids := m.w.order
	if len(ids) == 0 && len(m.w.chartOrder) == 0 {
		if !m.w.rosterRead && m.run != nil {
			p.add(m.st.dim("reading the store…"))
			return p
		}
		p.add(m.st.dim("no tickets yet"))
		p.add(m.st.dim("ask your agent to plan one, or make one with `jig ticket new --title \"...\"`"))
		if m.followNew {
			p.add(m.st.dim("the first ticket a session creates opens here"))
		}
		return p
	}

	// The tickets and the charts are one table, so a chart's name sits in the
	// column of the ticket ids above it.
	var refs []rowRef
	var cells [][]string
	for _, id := range ids {
		t := m.w.tickets[id]
		steps := m.steps(t)
		state := steps[currentStep(steps)].State
		refs = append(refs, rowRef{kind: rowTicket, id: id, ticket: id})
		cells = append(cells, []string{m.st.icon(state), id, t.title, m.stepDots(steps), m.ticketDetail(t, steps)})
	}
	nTickets := len(refs)
	chartRefs, chartCells := m.mainChartCells()
	refs, cells = append(refs, chartRefs...), append(cells, chartCells...)

	laid := layout(mainColumns, cells, max(1, m.width-2))
	for i, lines := range laid {
		if i == nTickets && len(chartRefs) > 0 {
			if nTickets > 0 {
				p.add("")
			}
			p.add(m.st.dim("charts"))
		}
		p.addLaid(&refs[i], lines)
	}
	return p
}

// mainColumns are the columns of main's rows: the state icon, the id or name,
// the title, the step dots (or "chart") and what stands.
var mainColumns = []column{{}, {id: true, gap: 1}, {free: true, gap: 2}, {gap: 2}, {free: true, gap: 2}}

// stepDots are a ticket's steps as icons, in order.
func (m *Model) stepDots(steps []stepRow) string {
	parts := make([]string, len(steps))
	for i, s := range steps {
		parts[i] = m.st.icon(s.State)
	}
	return strings.Join(parts, " ")
}

// ticketDetail is the words after a ticket's steps on the main screen: its
// current step and where that stands, or what waits for a person.
func (m *Model) ticketDetail(t *ticket, steps []stepRow) string {
	return m.ticketDetailIn(t, steps, true)
}

// ticketDetailIn is ticketDetail with or without the kind the ticket says of
// itself.
func (m *Model) ticketDetailIn(t *ticket, steps []stepRow, withKind bool) string {
	end, atEnd := m.endOf(t)
	kind := ""
	if k := t.digest.kindWords(); withKind && k != "" {
		kind = m.st.dim(k + " · ")
	}
	if atEnd && end.State == StateClosed {
		return kind + m.st.state(end.State, end.Words)
	}
	// A question is the person's whoever else is at work, so a live session
	// never holds its count back.
	if n := len(t.openQuestions(m.zone, "")); n > 0 {
		return m.st.state(StateNeedsInput, strconv.Itoa(n)+" open "+plural(n, "question", "questions"))
	}
	if d := t.digest; d != nil && len(d.Phases) == 0 {
		// An issue or a multi-task has no phases: its own words say what it is.
		return m.st.state(steps[currentStep(steps)].State, steps[currentStep(steps)].Text)
	}
	if atEnd {
		return kind + m.st.state(end.State, end.Words)
	}
	cur := steps[currentStep(steps)]
	if cur.Done {
		return kind + m.st.state(cur.State, cur.Text)
	}
	return kind + m.st.dim(stepNames[cur.Step]+": ") + m.st.state(cur.State, cur.Text)
}
