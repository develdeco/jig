package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// activityLines is how many of a step's latest lines a screen shows.
const activityLines = 8

// ticketPage is one ticket: its four steps in order, each with where it
// stands now, what waits for a person, what to do next, and the activity of
// the step under the cursor (the current step until the cursor moves).
func (m *Model) ticketPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	steps := m.steps(t)
	cur := currentStep(steps)
	right := fmt.Sprintf("step %d of %d", cur+1, len(steps))
	if steps[len(steps)-1].Done {
		right = "done"
	}
	p.boxTop("Steps", right)
	for i, s := range steps {
		p.boxRow(rowRef{kind: s.Kind, ticket: id}, m.stepLine(s, i == cur))
	}
	p.boxEnd()

	qs := t.openQuestions(m.zone, "")
	asks := t.asks()
	if len(qs)+len(asks) > 0 {
		p.add("")
		p.add(m.st.bold("needs you"))
		for _, q := range qs {
			p.addRow(rowRef{kind: rowQuestion, id: q.ID, ticket: id, slice: q.Slice}, m.questionLine(t, q))
		}
		for i, line := range m.askLines(asks) {
			p.addRow(rowRef{kind: rowAsk, id: asks[i].ID, ticket: id}, line)
		}
	}

	if t.status != nil && len(t.status.Help) > 0 {
		p.add("")
		p.add(m.st.dim("next  ") + t.status.Help[0])
	}

	p.add("")
	m.activityPanel(&p, t, m.ticketFocus(steps, len(qs)))
	return p
}

// stepLine is a step's row: its icon, number and name, where it stands, and
// how long it took once done. A step not reached yet is drawn back.
func (m *Model) stepLine(s stepRow, current bool) string {
	name := pad(stepNames[s.Step], 8)
	num := strconv.Itoa(s.Step+1) + " "
	text := s.Text
	switch {
	case current:
		name, text = m.st.bold(name), m.st.state(s.State, text)
	case !s.Done:
		num, name, text = m.st.dim(num), m.st.dim(name), m.st.dim(text)
	}
	return m.st.icon(s.State) + " " + num + name + " " + m.st.dim(pad(s.Took, 7)) + " " + text
}

// ticketFocus is the step whose activity the ticket screen shows: the one
// under the cursor, the build for a question, the review for an ask, and the
// current step while the cursor follows it. The screen's rows are the steps,
// then the open questions, then the asks, so the cursor's place says which.
func (m *Model) ticketFocus(steps []stepRow, questions int) int {
	e := m.top()
	switch {
	case e.follow:
		return currentStep(steps)
	case e.sel < len(steps):
		return max(0, e.sel)
	case e.sel < len(steps)+questions:
		return stepBuild
	}
	return stepReview
}

// activityPanel adds a panel of the latest lines of step to p.
func (m *Model) activityPanel(p *page, t *ticket, step int) {
	lines := t.stepLines(step)
	right := ""
	if n := len(lines); n > activityLines {
		right = fmt.Sprintf("latest %d of %d", activityLines, n)
		lines = lines[n-activityLines:]
	}
	p.boxTop(stepTitle(step)+" · activity", right)
	if len(lines) == 0 {
		p.boxAdd(m.st.dim("nothing yet"))
	}
	for _, l := range lines {
		p.boxAdd(m.activityLine(t, l))
	}
	p.boxEnd()
}

// questionLine is an open question as a row: who asked about which slice, and
// the question.
func (m *Model) questionLine(t *ticket, q questionRow) string {
	who := firstOf(m.who(t, q.Session), "a session")
	return m.st.icon(StateNeedsInput) + " " + who + " asks about slice " + q.Slice + ": " + firstLine(q.Text) + m.st.dim("  "+q.ID)
}

// firstLine is the first line of text.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// askLines are the asks as rows with their columns aligned: the icon (the
// ask's decision), risk, place and title, what has been decided, and the id.
func (m *Model) askLines(asks []askRow) []string {
	riskW, whereW := 0, 0
	for _, a := range asks {
		riskW, whereW = max(riskW, width(a.Risk)), max(whereW, width(a.Where))
	}
	out := make([]string, len(asks))
	for i, a := range asks {
		state := StateAsk
		decision := m.st.dim("undecided")
		switch a.Decision {
		case "keep":
			state, decision = StateKept, m.st.state(StateKept, "kept"+decidedBy(a))
		case "dismiss":
			state, decision = StateDismissed, m.st.state(StateDismissed, "dismissed"+decidedBy(a))
		}
		out[i] = fmt.Sprintf("%s %s  %s  %s  %s%s", m.st.icon(state), pad(a.Risk, riskW), pad(a.Where, whereW), a.Title, decision, m.st.dim("  "+a.ID))
	}
	return out
}

func decidedBy(a askRow) string {
	switch a.DecidedBy {
	case "":
		return ""
	case "person":
		return " by you"
	}
	return " by an " + a.DecidedBy
}
