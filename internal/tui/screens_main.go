package tui

import (
	"strconv"
)

// mainPage is the project: one row per ticket, with where it stands and the
// latest thing that happened to it.
func (m *Model) mainPage() page {
	var p page
	ids := m.w.order
	if len(ids) == 0 {
		p.add(m.st.dim("no tickets yet"))
		p.add(m.st.dim("make one with `jig ticket new --title \"...\"`"))
		return p
	}

	type row struct {
		id, title, label, detail, state string
	}
	rows := make([]row, 0, len(ids))
	idW, titleW, labelW := 4, 0, 0
	for _, id := range ids {
		t := m.w.tickets[id]
		state, label := t.phase(m.zone)
		r := row{id: id, title: t.title, label: label, state: state, detail: m.ticketDetail(t, state)}
		rows = append(rows, r)
		idW = max(idW, width(id))
		titleW = max(titleW, width(r.title))
		labelW = max(labelW, width(label))
	}
	titleW = min(titleW, max(0, (m.width-idW-labelW-12)/3))

	for _, r := range rows {
		line := m.st.icon(r.state) + " " + pad(r.id, idW)
		if titleW > 0 {
			line += "  " + pad(r.title, titleW)
		}
		line += "  " + m.st.state(r.state, pad(r.label, labelW)) + "  " + m.st.dim(r.detail)
		p.addRow(rowRef{kind: rowTicket, id: r.id, ticket: r.id}, line)
	}
	return p
}

// ticketDetail is the few words after a ticket's state on the main screen:
// what waits for a person, else the latest thing that happened.
func (m *Model) ticketDetail(t *ticket, state string) string {
	switch state {
	case StatePublished:
		if pub := t.publish(); pub.PR != "" {
			return pub.PR
		}
	case StateNeedsInput:
		if n := len(t.openQuestions(m.zone, "")); n > 0 {
			return strconv.Itoa(n) + " open " + plural(n, "question", "questions")
		}
	case StateFindings:
		if asks := t.asks(); len(asks) > 0 {
			return strconv.Itoa(len(asks)) + " " + plural(len(asks), "ask", "asks") + " to decide"
		}
	}
	if n := len(t.lines); n > 0 {
		l := t.lines[n-1]
		return clock(l, m.zone) + " " + describe(l)
	}
	return ""
}
