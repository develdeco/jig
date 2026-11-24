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
	if m.searchApplied() {
		// A search draws the rows that match, every group open, and no row of it
		// is folded away.
		m.folded = nil
		return m.searchOf(c).pg
	}
	m.folded = c.folded
	return c.pg
}

// buildMain draws the queue's page: the groups in order, each under its heading.
func (m *Model) buildMain(q mainQueue, open map[string]bool) (page, map[rowIdentity]rowIdentity, map[rowIdentity][]string) {
	p, folded := m.draftMain(q, open)
	return m.layMain(p), folded, p.memo
}

// mainDraft is the project's page before its rows are laid out: its headings
// and fold rows as lines, and a line for each ticket or chart row that stands
// for the cells the row says.
type mainDraft struct {
	page
	// cells is the cells of the row drawn at a line, by line.
	cells map[int][]string
	// chart says the row at a line is a chart's, laid out by its own kind of
	// column: a chart's name is not a ticket's id.
	chart map[int]bool
	// memo is the cells of every row the draft holds, by identity, which the
	// search reads instead of building a row's cells and detail again.
	memo map[rowIdentity][]string
}

// draftMain is buildMain's page of headings and rows, the rows still to be laid
// out by layMain.
func (m *Model) draftMain(q mainQueue, open map[string]bool) (*mainDraft, map[rowIdentity]rowIdentity) {
	p := &mainDraft{cells: map[int][]string{}, chart: map[int]bool{}, memo: map[rowIdentity][]string{}}
	var folded map[rowIdentity]rowIdentity
	if len(q.rows) == 0 {
		p.add(m.st.dim("no tickets yet"))
		p.add(m.st.dim("ask your agent to plan one, or make one with `jig ticket new --title \"...\"`"))
		if m.followNew {
			p.add(m.st.dim("the first ticket a session creates opens here"))
		}
		return p, nil
	}
	if q.byChart {
		for _, s := range q.sections {
			if len(p.lines) > 0 {
				p.add("")
			}
			m.addQueueRow(p, s)
			m.addQueueRows(p, s.kids)
		}
		if n := q.rowsIn(); n > 0 {
			if len(p.lines) > 0 {
				p.add("")
			}
			p.add(m.st.dim("no chart (" + strconv.Itoa(n) + ")"))
		}
	}
	for g := mainGroup(0); g < groupCount; g++ {
		rows := q.byGroup[g]
		if len(rows) == 0 {
			continue
		}
		if q.byChart && g != groupDone {
			m.addQueueRows(p, rows)
			continue
		}
		if len(p.lines) > 0 && !q.byChart {
			p.add("")
		}
		if !g.folds() {
			p.add(m.st.dim(g.heading(len(rows))))
			m.addQueueRows(p, rows)
			continue
		}
		fold := rowRef{kind: rowFold, id: g.name()}
		if open[g.name()] {
			p.addRow(fold, m.st.dim("▾ "+g.heading(len(rows))+" · enter folds them"))
			m.addQueueRows(p, rows)
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

// addQueueRows adds the rows of one group.
func (m *Model) addQueueRows(p *mainDraft, rows []queueRow) {
	for _, r := range rows {
		m.addQueueRow(p, r)
		m.addQueueRows(p, r.kids)
	}
}

// addQueueRow adds a row of the queue as the cells it says; layMain lays them out.
func (m *Model) addQueueRow(p *mainDraft, r queueRow) {
	at := len(p.lines)
	p.addRow(r.ref(), "")
	p.cells[at], p.chart[at] = m.queueCells(r), r.chart
	p.memo[r.ref().identity()] = p.cells[at]
}

// mainColumns are the columns of the project's rows: the state icon, the id or
// the name, the title, the step dots (or "chart") and what stands.
var mainColumns = []column{{}, {id: true, gap: 1}, {free: true, gap: 2}, {gap: 2}, {free: true, gap: 2}}

// layMain lays the draft's rows out, a column to the widest cell of its own
// kind: the tickets' ids are measured over the tickets, and a chart's name never
// widens them. A row that wraps keeps the cursor on its first line.
func (m *Model) layMain(p *mainDraft) page {
	var tickets, charts [][]string
	for i := range p.lines {
		if cells, ok := p.cells[i]; ok {
			if p.chart[i] {
				charts = append(charts, cells)
			} else {
				tickets = append(tickets, cells)
			}
		}
	}
	m.measured += len(tickets) + len(charts)
	w := max(1, m.width-2)
	laidT, laidC := layout(mainColumns, tickets, w), layout(mainColumns, charts, w)
	var out page
	for i, l := range p.lines {
		if _, ok := p.cells[i]; !ok {
			out.lines = append(out.lines, l)
			continue
		}
		var lines []string
		if p.chart[i] {
			lines, laidC = laidC[0], laidC[1:]
		} else {
			lines, laidT = laidT[0], laidT[1:]
		}
		out.addLaid(l.ref, lines)
	}
	return m.wrapLong(out)
}

// queueCells are a row of the queue as cells: a ticket's icon, id, title, the
// five steps and where it stands, or a chart's.
func (m *Model) queueCells(r queueRow) []string {
	if r.chart {
		return m.chartCells(r.id)
	}
	return []string{m.st.icon(r.cur.State), spaces(2*r.depth) + r.id, r.t.title, m.stepDots(r.steps), m.ticketDetail(r.t, r.steps)}
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
	m.detailBuilds++
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
