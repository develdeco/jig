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
	if len(ids) == 0 && len(m.w.chartOrder) > 0 {
		m.mainCharts(&p, 4, 0)
		return p
	}
	if len(ids) == 0 && !m.w.rosterRead && m.run != nil {
		p.add(m.st.dim("reading the store…"))
		return p
	}
	if len(ids) == 0 {
		p.add(m.st.dim("no tickets yet"))
		p.add(m.st.dim("ask your agent to plan one, or make one with `jig ticket new --title \"...\"`"))
		if m.followNew {
			p.add(m.st.dim("the first ticket a session creates opens here"))
		}
		return p
	}

	type row struct {
		id, title, dots, detail, state string
	}
	rows := make([]row, 0, len(ids))
	idW, titleW := 4, 0
	for _, id := range ids {
		t := m.w.tickets[id]
		steps := m.steps(t)
		r := row{id: id, title: t.title, dots: m.stepDots(steps), detail: m.ticketDetail(t, steps), state: steps[currentStep(steps)].State}
		rows = append(rows, r)
		idW = max(idW, width(id))
		titleW = max(titleW, width(r.title))
	}
	titleW = min(titleW, max(0, (m.width-idW-20)/3))

	for _, r := range rows {
		line := m.st.icon(r.state) + " " + pad(r.id, idW)
		if titleW > 0 {
			line += "  " + pad(r.title, titleW)
		}
		line += "  " + r.dots + "  " + r.detail
		p.addRow(rowRef{kind: rowTicket, id: r.id, ticket: r.id}, line)
	}
	m.mainCharts(&p, idW, titleW)
	return p
}

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
	end, atEnd := m.endOf(t)
	kind := ""
	if k := t.digest.kindWords(); k != "" {
		kind = m.st.dim(k + " · ")
	}
	if atEnd && end.State == StateClosed {
		return kind + m.st.state(end.State, end.Words)
	}
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
