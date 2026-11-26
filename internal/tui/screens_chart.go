package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/phase"
)

// activityFloor is the fewest rows the chart screen's Activity panel gets with
// its frame: the newest two lines.
const activityFloor = 5

// mapLabelW is the width of the label column of the Map panel.
const mapLabelW = 12

// chartPage is one chart: its map, its tickets in the order the chart will run
// them, the ticket under the cursor in detail, what a run would do now, and the
// latest of what the tickets did. The map is the first thing a short screen
// gives up, then the Run panel's detail, then the Detail panel's rows down to
// one; the tickets are never cut, a longer page scrolls with the cursor.
func (m *Model) chartPage(name string) page {
	var p page
	c := m.w.charts[name]
	if c == nil {
		p.add(m.st.dim("no chart " + name))
		return p
	}
	if c.issueID() != "" {
		return m.planPage(c)
	}
	rows := m.chartRows(c)
	tickets := m.chartTicketsPanel(c, rows)

	// The ticket detailed is the one under the cursor. The cursor is read as
	// View clamps it, and never through selected() or page(), which call this.
	var detail []runRow
	detailID := ""
	if len(rows) > 0 {
		r := rows[m.ticketsCursor(rows, "Tickets")]
		detail, detailID = m.chartDetailRows(r), r.id
		if !m.zoomed("Detail") {
			detail = trimRuns(detail, detailMax)
		}
	}
	// The heights: the tickets, the Detail and Run panels and the activity at
	// its floor come first, and the Run panel's detail goes before the Detail
	// panel's, which goes before the tickets are cut; what is left is the map's.
	frame := func(n int) int {
		if n == 0 {
			return 0
		}
		return 2 + n
	}
	var run []runRow
	if c.info != nil {
		run = m.runRows(c.info)
		if age, live := c.live(m.w, m.now()); live {
			run = m.runningRows(run, c.info, age)
		}
	}
	spare := m.bodyRows() - len(tickets.lines) - frame(m.runHeight(detail)) - frame(m.runHeight(run)) - activityFloor
	if spare < 0 && len(run) > 1 && !m.zoomed("Run") {
		cut := min(-spare, len(run)-1)
		run = trimRuns(run, len(run)-cut)
		spare += cut
	}
	if spare < 0 && !m.zoomed("Detail") {
		var cut int
		detail, cut = shortenRuns(detail, -spare)
		spare += cut
	}
	var mapPanel page
	if c.info != nil {
		if m.zoomed("Map") {
			spare = m.bodyRows()
		}
		m.mapPanel(&mapPanel, c, spare)
	}
	p.lines = append(p.lines, mapPanel.lines...)
	p.lines = append(p.lines, tickets.lines...)
	if len(detail) > 0 {
		m.detailPanel(&p, detailID, detail)
	}
	if c.info != nil {
		p.boxTop("Run", runWord(c.info))
		m.runBoxRows(&p, run)
		p.boxEnd()
	}
	var history []chartCommit
	if c.info != nil {
		history = c.info.History
	}
	m.chartActivityPanel(&p, "chart:"+c.listing.Name, rows, history)
	return p
}

// chartTicketsPanel is the chart's tickets, a row each the cursor can stand on:
// the ticket's state, id and title, its five steps, and a note on where it
// stands in the chart. The note's column is sized first, so the words that say
// what a ticket waits for are never the part cut.
func (m *Model) chartTicketsPanel(c *chart, rows []chartRow) page {
	total, done, _ := chartProgress(rows)
	right := fmt.Sprintf("%d of %d published", done, total)
	if total == 0 {
		right = "none graduated"
	}
	return m.ticketRowsPanel(c, rows, "Tickets", right, m.chartNote, nil)
}

// ticketRowsPanel is the panel of a chart's tickets as chartTicketsPanel draws
// it, under title and with right at its edge: noteOf says the words after each
// row's steps, and sub, when set, the lines a row has under it that the cursor
// does not stand on. A task under another has its id indented. The rows are
// laid out together, so the ids, titles and steps line up, and a note or a
// title that does not fit wraps under its column.
func (m *Model) ticketRowsPanel(c *chart, rows []chartRow, title, right string, noteOf func(chartRow) string, sub func(chartRow) []string) page {
	var p page
	p.boxTop(title, right)

	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		age := ""
		if r.t != nil {
			age = m.st.dim(m.ageOf(r.t.lastActivity()))
		}
		cells = append(cells, []string{m.st.icon(r.state()), spaces(2*r.depth) + r.id, r.title, m.chartDots(r), noteOf(r), age})
	}
	// The entries that have no ticket yet: fog, listed after the tickets.
	nFog := 0
	if c != nil && c.info != nil {
		for _, t := range c.info.Tickets {
			if t.ID == "" {
				cells = append(cells, []string{m.st.dim("·"), "", m.st.dim(t.Title), "", m.st.dim("fog · not graduated"), ""})
				nFog++
			}
		}
	}
	var laid [][]string
	if len(cells) > 0 {
		names := make([]string, len(ticketColumnNames))
		for i, n := range ticketColumnNames {
			names[i] = m.st.dim(n)
		}
		laid = m.laidList(&p, ticketColumns, names, cells, 0)
	}
	for i, r := range rows {
		p.boxLaid(&rowRef{kind: rowTicket, id: r.id, ticket: r.id}, laid[i])
		if sub != nil {
			for _, l := range sub(r) {
				p.boxAdd(l)
			}
		}
	}
	for _, lines := range laid[len(rows):] {
		p.boxLaid(nil, lines)
	}
	if c != nil && c.info == nil && c.listing.Ungraduated > 0 {
		n := c.listing.Ungraduated
		nFog++
		p.boxAdd(m.st.dim("· " + strconv.Itoa(n) + " " + plural(n, "entry", "entries") + " not graduated"))
	}
	if len(rows)+nFog == 0 {
		p.boxAdd(m.st.dim("no tickets yet"))
	}
	p.boxEnd()
	return p
}

// ticketColumns are the columns of a panel's ticket rows: the state icon
// (indented under its parent), the id, the title, the five steps, the note and
// the age, which is never cut.
var ticketColumns = []column{{}, {id: true, gap: 1}, {free: true, gap: 2}, {gap: 2}, {free: true, gap: 2}, {gap: 2, keep: true}}

// chartNote is the words after a ticket's steps in the chart: what it waits
// for when its build has not begun, that it can start, or where it stands as
// on the project's list.
func (m *Model) chartNote(r chartRow) string { return m.chartNoteIn(r, true) }

// chartNoteIn is chartNote with or without the kind a ticket says of itself
// ("task of <issue> · "), which a screen that is about that parent need not say.
func (m *Model) chartNoteIn(r chartRow, kind bool) string {
	switch {
	case r.verb != nil && r.verb.Problem != "":
		return m.st.state(StateStalled, r.verb.Problem)
	case r.t == nil || r.t.digest == nil:
		// The roster has not listed the ticket: nothing of it is known.
		return m.st.dim(readingText)
	case r.behind:
		return m.st.state(StateReady, behindWord) + m.st.dim(" · "+behindOwes)
	case r.t != nil && (r.done || r.needs || len(r.t.stepLines(stepBuild)) > 0):
		return m.ticketDetailIn(r.t, r.steps, kind)
	case r.done:
		return m.st.state(StatePublished, "published")
	case len(r.waits) > 0:
		return m.st.dim(waitNote(r.waits[0]))
	case r.t != nil && r.t.digest != nil && r.t.digest.Ready:
		return m.st.state(StateReady, "ready") + m.st.dim(" · "+r.t.digest.Current+" next")
	case r.t != nil:
		return m.ticketDetailIn(r.t, r.steps, kind)
	}
	return ""
}

// runRow is a line of the Run panel: a label, the words, and how much the line
// matters when the panel is short (0 is kept longest). A line of an ask's text
// carries the ask's id and is never cut for room: a person decides the ask from
// it.
type runRow struct {
	label, text string
	weight      int
	ask         string
}

// cuttable is how many of rows a short panel may give up: all but the lines of
// an ask.
func cuttable(rows []runRow) int {
	n := 0
	for _, r := range rows {
		if r.ask == "" {
			n++
		}
	}
	return n
}

// runRows are what a run of the chart would do: the prompt that starts it,
// what it briefs, the waves it builds, where it ends, and last what it has
// cost so far.
func (m *Model) runRows(info *chartInfo) []runRow {
	next := m.st.dim("nothing to run")
	switch {
	case info.Prompt != "":
		next = "say " + m.st.key(strconv.Quote(info.Prompt))
	case info.Reason != "":
		next = m.st.dim(info.Reason)
	}
	rows := []runRow{{"next", next, 0, ""}}
	first, then := landingsOf(info)
	switch {
	case first != nil:
		// The run stops at a landing: the person's word is the landing's, not
		// the run's, and the reason is the landing's state as phase words it.
		rows[0].text = "say " + m.st.key(strconv.Quote(first.Prompt))
		rows = append(rows, runRow{"why", m.st.dim(first.State), 1, ""})
		for _, l := range then {
			words := "also " + l.Prompt
			if len(l.After) > 0 {
				words = "then " + l.Prompt + ", after " + andList(l.After)
			}
			rows = append(rows, runRow{"", m.st.dim(words), 1, ""})
		}
	case len(info.Ends) == 0 && info.Reason != "" && info.Prompt != "":
		rows = append(rows, runRow{"why", m.st.dim(info.Reason), 1, ""})
	}
	if len(info.Briefs) > 0 {
		rows = append(rows, runRow{"briefs", strings.Join(info.Briefs, ", "), 2, ""})
	}
	for i, w := range info.Run {
		rows = append(rows, runRow{"wave " + strconv.Itoa(i+1), strings.Join(w, ", "), 3, ""})
	}
	for i, e := range info.Ends {
		label := ""
		if i == 0 {
			label = "ends at"
		}
		note := endNote(e)
		if info.Model && e.Kind != kindStacked && e.Fact != "" {
			// An issue's run waits for a landing, not a merge: the verb's fact is
			// the words internal/phase wrote for it.
			note = e.Fact
		}
		rows = append(rows, runRow{label, note, 1, ""})
	}
	if info.Spend != nil {
		rows = append(rows, runRow{"spend", spendWords(info.Spend), 2, ""})
	}
	return rows
}

// runningRows are the Run panel's rows while a session is at work on the run:
// the next row says so and how long ago it was last seen, in place of the
// prompt that would start another run. A landing's prompt is the person's word,
// which a session merges at, so it is never held back.
func (m *Model) runningRows(rows []runRow, info *chartInfo, age time.Duration) []runRow {
	if first, _ := landingsOf(info); first != nil || len(rows) == 0 {
		return rows
	}
	rows = append([]runRow(nil), rows...)
	rows[0].text = m.st.dim("running · a session worked on it " + ago(age) + " ago")
	return rows
}

// landingsOf are the landings of a run that stopped at one: the first the
// person may ask for now (the first that waits for no other), and the ones after
// it. Nil when the run is not at a landing stop or no landing is open to ask.
func landingsOf(info *chartInfo) (first *chartLand, then []chartLand) {
	if info.Code != phase.CodeChartWaitsLand {
		return nil, nil
	}
	for i := range info.Lands {
		l := info.Lands[i]
		if first == nil && len(l.After) == 0 {
			first = &info.Lands[i]
			continue
		}
		then = append(then, l)
	}
	if first == nil {
		return nil, nil
	}
	return first, then
}

// spendWords is what a chart's run has cost, in the parts the Run panel's row
// joins with " · ": the tokens, the dollars when a model has a price, how many
// tickets and subagents it spans, and the models that have no price last.
func spendWords(s *chartSpend) string {
	parts := []string{tokens(s.Tokens) + " tokens"}
	if s.USD > 0 {
		parts = append(parts, dollars(s.USD))
	}
	parts = append(parts, strconv.Itoa(s.Tickets)+" "+plural(s.Tickets, "ticket", "tickets"))
	if s.Agents > 0 {
		parts = append(parts, strconv.Itoa(s.Agents)+" "+plural(s.Agents, "subagent", "subagents"))
	}
	if len(s.Unpriced) > 0 {
		parts = append(parts, "no price for "+andList(s.Unpriced))
	}
	return strings.Join(parts, " · ")
}

// andList joins words as prose, as the CLI's does for the models of its spend line:
// "a", "a and b", "a, b and c".
func andList(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// shortenRuns is rows with up to short lines given back, and how many were: one
// line of the cuttable ones is always kept, and the lines of an ask are never
// given back.
func shortenRuns(rows []runRow, short int) ([]runRow, int) {
	cut := min(short, cuttable(rows)-1)
	if cut < 1 {
		return rows, 0
	}
	return trimRuns(rows, cuttable(rows)-cut), cut
}

// trimRuns is rows cut to n lines, the least important first (the last of
// them among equals), the rest in their order. The lines of an ask are not
// counted in n and never cut.
func trimRuns(rows []runRow, n int) []runRow {
	rows = append([]runRow(nil), rows...)
	for cuttable(rows) > max(1, n) {
		drop := -1
		for i, r := range rows {
			if r.ask == "" && (drop < 0 || r.weight >= rows[drop].weight) {
				drop = i
			}
		}
		rows = append(rows[:drop], rows[drop+1:]...)
	}
	return rows
}

// mapPanel adds the chart's map to p in the room rows it has: the panel when
// there are three or more, one line of counts for fewer, nothing for none.
func (m *Model) mapPanel(p *page, c *chart, room int) {
	mp := c.info.Map
	switch {
	case room < 1 || (room < 3 && mp == nil):
		return
	case room < 3:
		p.add(m.st.dim(fmt.Sprintf("map: %d decided · %d fog", len(mp.Decided), len(mp.Fog))))
		return
	}
	p.boxTop("Map", "charts/"+c.listing.Name+"/map.md")
	if mp == nil {
		p.boxAdd(m.st.dim("no map.md yet"))
	} else {
		for _, l := range m.mapLines(mp, m.width-7, room-2) {
			p.boxAdd(l)
		}
	}
	p.boxEnd()
}

// mapLines are the map's sections as lines in n rows, w cells wide: the
// destination, what was decided (✓), what is still fog (~) and what is out of
// scope. When they do not all fit the out-of-scope lines go first, then the
// decisions and the fog shrink to their first item and a count, then the
// destination to one line, then the decisions and the fog go.
func (m *Model) mapLines(mp *chartMap, w, n int) []string {
	textW := max(1, w-mapLabelW-1)
	type section struct {
		label string
		full  []string // every line, at its fullest
		one   []string // one line, the rest counted
	}
	items := func(label, glyph string, list []string, most int, tint func(string) string) section {
		s := section{label: label}
		line := func(item string) string { return tint(glyph) + " " + item }
		for i, item := range list {
			if i == most-1 && len(list) > most {
				s.full = append(s.full, m.st.dim("+"+strconv.Itoa(len(list)-i)+" more"))
				break
			}
			s.full = append(s.full, line(item))
		}
		if len(list) > 0 {
			first := line(list[0])
			if len(list) > 1 {
				first += m.st.dim("  +" + strconv.Itoa(len(list)-1) + " more")
			}
			s.one = []string{first}
		}
		return s
	}
	plain := func(s string) string { return s }
	dest := section{label: "destination"}
	if text := strings.Join(mp.Destination, " "); text != "" {
		dest.full = wrap(text, textW, 2)
		dest.one = wrap(text, textW, 1)
	}
	secs := []section{
		dest,
		items("decided", "✓", mp.Decided, 4, func(s string) string { return m.st.state(StateOK, s) }),
		items("fog", "~", mp.Fog, 4, m.st.dim),
		items("out of scope", "·", mp.OutOfScope, 3, plain),
	}
	// What each reduction does, in the order they are made.
	const (
		dropScope = iota
		oneDecided
		oneFog
		oneDest
		dropDecided
		dropFog
		reductions
	)
	lines := func(level int) [][]string {
		out := make([][]string, len(secs))
		for i, s := range secs {
			out[i] = s.full
		}
		if level > dropScope {
			out[3] = nil
		}
		if level > oneDecided {
			out[1] = secs[1].one
		}
		if level > oneFog {
			out[2] = secs[2].one
		}
		if level > oneDest {
			out[0] = secs[0].one
		}
		if level > dropDecided {
			out[1] = nil
		}
		if level > dropFog {
			out[2] = nil
		}
		return out
	}
	count := func(by [][]string) int {
		total := 0
		for _, l := range by {
			total += len(l)
		}
		return total
	}
	by := lines(0)
	for level := 1; level <= reductions && count(by) > n; level++ {
		by = lines(level)
	}
	var out []string
	for i, l := range by {
		for j, text := range l {
			label := spaces(mapLabelW)
			if j == 0 {
				label = m.st.dim(pad(secs[i].label, mapLabelW))
			}
			out = append(out, label+" "+text)
		}
	}
	return out[:min(len(out), n)]
}

// chartLine is a line of the chart's activity: a ticket's log line, or (commit
// set) a commit of the store that touched the chart's folder. at is its instant
// either way.
type chartLine struct {
	id string
	t  *ticket
	placedLine
	commit *chartCommit
	at     time.Time
}

// The words of a commit's line: "chart" stands where a ticket's id does and
// "store" where its step does.
const (
	chartCommitID  = "chart"
	chartCommitTag = "store"
)

// tag is the line's place: the step it belongs to, or "store" for a commit.
func (c chartLine) tag() string {
	if c.commit != nil {
		return chartCommitTag
	}
	return tagOf(c.step, c.key)
}

// ticketsCursor is the place among rows of the ticket the cursor of the pane
// titled title stands on, whatever pane has the focus: the cursor itself when
// the pane has the focus, else the row it was left on. It reads no page.
func (m *Model) ticketsCursor(rows []chartRow, title string) int {
	e := m.top()
	last := max(len(rows)-1, 0)
	pc := e.paneCurs[title]
	if !pc.set {
		// The first frame: the cursor has not been read as a row yet.
		return min(max(e.sel, 0), last)
	}
	for i, r := range rows {
		if r.id == pc.id.id {
			return i
		}
	}
	return min(max(pc.idx, 0), last)
}

// chartFirstOpen is the position, among a chart screen's rows, of the first
// ticket that is not published: where the cursor starts.
func (m *Model) chartFirstOpen(c *chart) int {
	for i, r := range m.chartRows(c) {
		if !r.done {
			return i
		}
	}
	return 0
}
