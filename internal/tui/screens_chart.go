package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// activityFloor is the fewest rows the chart screen's Activity panel gets with
// its frame: the newest two lines.
const activityFloor = 4

// mapLabelW is the width of the label column of the Map panel.
const mapLabelW = 12

// chartPage is one chart: its map, its tickets in the order the chart will run
// them, what a run would do now, and the latest of what the tickets did. The
// map is the first thing a short screen gives up, then the Run panel's detail;
// the tickets are never cut, a longer page scrolls with the cursor.
func (m *Model) chartPage(name string) page {
	var p page
	c := m.w.charts[name]
	if c == nil {
		p.add(m.st.dim("no chart " + name))
		return p
	}
	rows := m.chartRows(c)
	tickets := m.chartTicketsPanel(c, rows)

	var run []runRow
	var mapPanel page
	if c.info != nil {
		// The heights: the tickets, the Run panel and the activity at its floor
		// come first, and the Run panel's detail goes before the tickets are
		// cut; what is left is the map's.
		run = m.runRows(c.info)
		spare := m.bodyRows() - len(tickets.lines) - (2 + len(run)) - activityFloor
		if spare < 0 {
			cut := min(-spare, len(run)-1)
			run = trimRuns(run, len(run)-cut)
			spare += cut
		}
		m.mapPanel(&mapPanel, c, spare)
	}
	p.lines = append(p.lines, mapPanel.lines...)
	p.lines = append(p.lines, tickets.lines...)
	if c.info != nil {
		p.boxTop("Run", runWord(c.info))
		for _, r := range run {
			p.boxAdd(m.st.dim(pad(r.label, 8)) + " " + r.text)
		}
		p.boxEnd()
	}
	m.chartActivityPanel(&p, rows)
	return p
}

// chartTicketsPanel is the chart's tickets, a row each the cursor can stand on:
// the ticket's state, id and title, its four steps, and a note on where it
// stands in the chart. The note's column is sized first, so the words that say
// what a ticket waits for are never the part cut.
func (m *Model) chartTicketsPanel(c *chart, rows []chartRow) page {
	var p page
	total, done, _ := chartProgress(rows)
	right := fmt.Sprintf("%d of %d published", done, total)
	if total == 0 {
		right = "none graduated"
	}
	p.boxTop("Tickets", right)

	// The entries that have no ticket yet: fog, listed after the tickets.
	var fog []string
	if c.info != nil {
		for _, t := range c.info.Tickets {
			if t.ID == "" {
				fog = append(fog, t.Title)
			}
		}
	}

	idW, titleW, noteW, ownW := 0, 0, 0, 0
	for _, title := range fog {
		titleW = max(titleW, width(title))
	}
	notes := make([]string, len(rows))
	for i, r := range rows {
		var own bool
		notes[i], own = m.chartNote(r)
		idW, titleW, noteW = max(idW, width(r.id)), max(titleW, width(r.title)), max(noteW, width(notes[i]))
		if own {
			ownW = max(ownW, width(notes[i]))
		}
	}
	// "icon id  title  ◐ ○ ○ ○  note" in the room a panel's text has. The words
	// the chart says itself keep their whole column, the title gives way to them,
	// and a status's own words are cut at the end of the line.
	room := m.width - 7 - (2 + idW + 2 + 2 + 2*stepCount - 1 + 2)
	noteW = max(ownW, min(noteW, room-min(titleW, 16)))
	titleW = min(titleW, max(0, room-noteW))
	for i, r := range rows {
		line := m.st.icon(r.state()) + " " + pad(r.id, idW)
		if titleW > 0 {
			line += "  " + pad(r.title, titleW)
		}
		line += "  " + m.chartDots(r) + "  " + notes[i]
		p.boxRow(rowRef{kind: rowTicket, id: r.id, ticket: r.id}, line)
	}

	for _, title := range fog {
		p.boxAdd(m.st.dim("·" + spaces(1+idW) + "  " + pad(title, titleW) + "  fog · not graduated"))
	}
	if n := c.listing.Ungraduated; c.info == nil && n > 0 {
		fog = append(fog, "")
		p.boxAdd(m.st.dim("· " + strconv.Itoa(n) + " " + plural(n, "entry", "entries") + " not graduated"))
	}
	if len(rows)+len(fog) == 0 {
		p.boxAdd(m.st.dim("no tickets yet"))
	}
	p.boxEnd()
	return p
}

// chartNote is the words after a ticket's steps in the chart: what it waits
// for when its build has not begun, that it can start, or where it stands as
// on the project's list. own says they are the chart's own words, which a
// narrow row must not cut, and not the ticket's status.
func (m *Model) chartNote(r chartRow) (note string, own bool) {
	switch {
	case r.verb != nil && r.verb.Problem != "":
		return m.st.state(StateStalled, r.verb.Problem), true
	case r.t != nil && (r.done || r.needs || len(r.t.stepLines(stepBuild)) > 0):
		return m.ticketDetail(r.t, r.steps), false
	case r.done:
		return m.st.state(StatePublished, "published"), true
	case len(r.waits) > 0:
		return m.st.dim(waitNote(r.waits[0])), true
	case r.verb != nil && r.verb.Ready:
		return m.st.state(StateReady, "ready") + m.st.dim(" · "+r.verb.Phase+" next"), true
	case r.t != nil:
		return m.ticketDetail(r.t, r.steps), false
	}
	return "", false
}

// runRow is a line of the Run panel: a label, the words, and how much the line
// matters when the panel is short (0 is kept longest).
type runRow struct {
	label, text string
	weight      int
}

// runRows are what a run of the chart would do: the prompt that starts it,
// what it briefs, the waves it builds, and where it ends.
func (m *Model) runRows(info *chartInfo) []runRow {
	next := m.st.dim("nothing to run")
	switch {
	case info.Prompt != "":
		next = "say " + m.st.key(strconv.Quote(info.Prompt))
	case info.Reason != "":
		next = m.st.dim(info.Reason)
	}
	rows := []runRow{{"next", next, 0}}
	if len(info.Ends) == 0 && info.Reason != "" && info.Prompt != "" {
		rows = append(rows, runRow{"why", m.st.dim(info.Reason), 1})
	}
	if len(info.Briefs) > 0 {
		rows = append(rows, runRow{"briefs", strings.Join(info.Briefs, ", "), 2})
	}
	for i, w := range info.Run {
		rows = append(rows, runRow{"wave " + strconv.Itoa(i+1), strings.Join(w, ", "), 3})
	}
	for i, e := range info.Ends {
		label := ""
		if i == 0 {
			label = "ends at"
		}
		rows = append(rows, runRow{label, endNote(e), 1})
	}
	return rows
}

// trimRuns is rows cut to n lines, the least important first (the last of
// them among equals), the rest in their order.
func trimRuns(rows []runRow, n int) []runRow {
	rows = append([]runRow(nil), rows...)
	for len(rows) > max(1, n) {
		drop := 0
		for i, r := range rows {
			if r.weight >= rows[drop].weight {
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

// chartLine is a line of the chart's activity: a ticket's log line.
type chartLine struct {
	id string
	t  *ticket
	placedLine
}

// chartActivityPanel adds the latest lines of every ticket of the chart to p,
// newest first, each with the ticket and the step it belongs to: as many as the
// rows left below p allow, two at least.
func (m *Model) chartActivityPanel(p *page, rows []chartRow) {
	var all []chartLine
	idW := 0
	for _, r := range rows {
		if r.t == nil {
			continue
		}
		idW = max(idW, width(r.id))
		for _, pl := range m.placed(r.t, r.d) {
			all = append(all, chartLine{r.id, r.t, pl})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, _ := all[i].l.At()
		b, _ := all[j].l.At()
		return a.Before(b)
	})
	keep := max(activityFloor-2, m.bodyRows()-len(p.lines)-2)
	right := ""
	if n := len(all); n > keep {
		right = fmt.Sprintf("latest %d of %d", keep, n)
	}
	p.boxTop("Activity", right)
	if len(all) == 0 {
		p.boxAdd(m.st.dim("nothing yet"))
	}
	var shown []chartLine
	tagW := 0
	for i := len(all) - 1; i >= 0 && len(shown) < keep; i-- {
		tagW = max(tagW, min(tagMaxW, width(tagOf(all[i].step, all[i].key))))
		shown = append(shown, all[i])
	}
	for _, e := range shown {
		tag := pad(tagOf(e.step, e.key), tagW)
		if e.step >= 0 {
			tag = m.st.state(stepTagState[e.step], tag)
		} else {
			tag = m.st.dim(tag)
		}
		p.boxAdd(m.st.dim(clock(e.l, m.zone)) + " " + pad(e.id, idW) + " " + tag + " " + m.moveText(e.t, e.l))
	}
	p.boxEnd()
}

// mainCharts adds the project's charts to p after its tickets: a row each, with
// how many of its tickets are published and whether one needs the person. The
// columns follow the tickets' (idW, titleW) and widen for a longer name.
func (m *Model) mainCharts(p *page, idW, titleW int) {
	if len(m.w.chartOrder) == 0 {
		return
	}
	nameW := idW
	for _, name := range m.w.chartOrder {
		nameW = max(nameW, width(name))
	}
	if titleW == 0 {
		for _, name := range m.w.chartOrder {
			titleW = max(titleW, width(m.w.charts[name].title()))
		}
		titleW = min(titleW, max(0, (m.width-nameW-20)/3))
	}
	if len(p.lines) > 0 {
		p.add("")
	}
	p.add(m.st.dim("charts"))
	for _, name := range m.w.chartOrder {
		c := m.w.charts[name]
		rows := m.chartRows(c)
		total, done, needs := chartProgress(rows)
		state, _ := m.chartStatus(c, rows)
		title := c.title()
		if title == name {
			title = ""
		}
		line := m.st.icon(state) + " " + pad(name, nameW)
		if titleW > 0 {
			line += "  " + pad(title, titleW)
		}
		line += "  " + m.st.dim("chart") + "  "
		if total == 0 {
			line += m.st.dim("not graduated")
		} else {
			line += m.st.dim(fmt.Sprintf("%d of %d published", done, total))
		}
		if needs > 0 {
			line += m.st.state(StateNeedsInput, fmt.Sprintf(" · %d needs you", needs))
		}
		p.addRow(rowRef{kind: rowChart, id: name}, line)
	}
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
