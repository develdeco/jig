package tui

import (
	"strconv"
	"strings"
)

// mainPage is the project as a queue of what the person does next: its tickets
// and the charts that plan no issue, each in the first group that holds for it
// (mainqueue.go), a heading with its count over each group, and a fold row for
// the groups that are mostly history. It is built once for what it reads (the
// roster, the stream, the clock's bucket, the width and the folds that are
// open), and a frame that changed none of them draws the page it built.
func (m *Model) mainPage() page {
	if m.readingStore() {
		// Main draws from the roster and from nothing else: the stream's replay
		// names the tickets in the order it arrives, and rows drawn from it
		// would all move when the list lands.
		var p page
		p.add(m.st.dim("reading the store…"))
		return p
	}
	c := m.mainBuilt()
	m.folded = c.folded
	return c.pg
}

// buildMain draws the queue's page: the groups in order, each under its heading.
func (m *Model) buildMain(q mainQueue, open map[string]bool) (page, map[rowIdentity]rowIdentity) {
	var p page
	var folded map[rowIdentity]rowIdentity
	if len(q.rows) == 0 {
		p.add(m.st.dim("no tickets yet"))
		p.add(m.st.dim("ask your agent to plan one, or make one with `jig ticket new --title \"...\"`"))
		if m.followNew {
			p.add(m.st.dim("the first ticket a session creates opens here"))
		}
		return p, nil
	}
	for g := mainGroup(0); g < groupCount; g++ {
		rows := q.byGroup[g]
		if len(rows) == 0 {
			continue
		}
		if len(p.lines) > 0 {
			p.add("")
		}
		if !g.folds() {
			p.add(m.st.dim(g.heading(len(rows))))
			m.addQueueRows(&p, q, rows)
			continue
		}
		fold := rowRef{kind: rowFold, id: g.name()}
		if open[g.name()] {
			p.addRow(fold, m.st.dim("▾ "+g.heading(len(rows))+" · enter folds them"))
			m.addQueueRows(&p, q, rows)
			continue
		}
		tasks := 0
		for _, r := range rows {
			tasks += r.tasks()
		}
		with := ""
		if tasks > 0 {
			// The tasks of an issue are in its row, folded with it.
			with = " · " + strconv.Itoa(tasks) + " " + plural(tasks, "task", "tasks") + " in them"
		}
		p.addRow(fold, m.st.dim("▸ "+g.heading(len(rows))+with+" · enter shows them"))
		if folded == nil {
			folded = map[rowIdentity]rowIdentity{}
		}
		for _, r := range rows {
			foldRow(folded, r, fold.identity())
		}
	}
	return p, folded
}

// foldRow notes that the row and the rows under it are in the fold.
func foldRow(folded map[rowIdentity]rowIdentity, r queueRow, fold rowIdentity) {
	folded[r.ref().identity()] = fold
	for _, k := range r.kids {
		foldRow(folded, k, fold)
	}
}

// readingStore is whether the screens have a runner and the store's ticket list
// has not been read: nothing is known of what the store holds. A model with no
// runner (a test, or a screen fed by the stream alone) draws the stream.
func (m *Model) readingStore() bool { return m.run != nil && !m.w.rosterRead }

// addQueueRows draws the rows of one group.
func (m *Model) addQueueRows(p *page, q mainQueue, rows []queueRow) {
	for _, r := range rows {
		p.addRow(r.ref(), m.queueLine(q, r))
		m.addQueueRows(p, q, r.kids)
	}
}

// queueLine is a row of the queue: a ticket's icon, id, title, the five steps and
// where it stands, or a chart's name, title and published count.
func (m *Model) queueLine(q mainQueue, r queueRow) string {
	if r.chart {
		return m.chartMainRow(r.id, q.chartIDW, q.chartTitleW)
	}
	indent := 2 * r.depth
	line := spaces(indent) + m.st.icon(r.cur.State) + " " + pad(r.id, q.idW-indent)
	if q.titleW > 0 {
		line += "  " + pad(r.t.title, q.titleW)
	}
	return line + "  " + m.stepDots(r.steps) + "  " + m.ticketDetail(r.t, r.steps)
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
		// An issue or a multi-task has no phases: its own words say what it is,
		// and at its landing stop what the plan says there.
		if st, words := m.planLanding(t.id, t); st == StateWaitsToLand {
			return kind + m.st.state(st, words)
		}
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
