package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
)

// The kinds of row a cursor can stand on.
const (
	rowChart    = "chart"
	rowTicket   = "ticket"
	rowBrief    = "brief"
	rowBuild    = "build"
	rowReview   = "review"
	rowPublish  = "publish"
	rowLand     = "land"
	rowQuestion = "question"
	rowAsk      = "ask"
	rowSlice    = "slice"
	// rowStop is a line the cursor can rest on and that opens nothing: the head
	// of a record on a screen whose page is longer than its window, so the
	// window can be moved to it.
	rowStop = "stop"
)

// rowRef says what a selectable line is: its kind, its id, and the ticket and
// slice it belongs to.
type rowRef struct {
	kind   string
	id     string
	ticket string
	slice  string
}

// pline is one line of a screen's body, with what it is when the cursor can
// stand on it, and its place in a panel (frame.go): box, and for a panel's
// top edge its title in text and right. A line of an ask's text carries the
// ask's id, so View can record whether it drew the ask whole (drawnAsks).
type pline struct {
	text  string
	ref   *rowRef
	box   int
	right string
	// cont marks a continuation row: a line a row's free text wrapped onto. It
	// belongs to the row above it, and the cursor never stands on it.
	cont bool
	ask  string
}

// page is a screen's body before it is windowed and framed.
type page struct {
	lines []pline
}

func (p *page) add(text string) { p.lines = append(p.lines, pline{text: text}) }

func (p *page) addRow(ref rowRef, text string) {
	p.lines = append(p.lines, pline{text: text, ref: &ref})
}

// addLaid adds the lines the allocator laid a row out in: the first is the
// row, with the cursor's ref when it has one, and the rest are its
// continuation rows.
func (p *page) addLaid(ref *rowRef, lines []string) {
	for i, l := range lines {
		pl := pline{text: l, cont: i > 0}
		if i == 0 {
			pl.ref = ref
		}
		p.lines = append(p.lines, pl)
	}
}

// refs are the selectable rows, top to bottom.
func (p page) refs() []rowRef {
	var out []rowRef
	for _, l := range p.lines {
		if l.ref != nil {
			out = append(out, *l.ref)
		}
	}
	return out
}

// page builds the body of the screen on top: its lines, with the text of any
// line too long for the width wrapped onto continuation rows.
func (m *Model) page() page {
	return m.wrapLong(m.rawPage())
}

// wrapLong wraps the lines of pg that are wider than the screen at words onto
// continuation rows under the line's own text, three rows at most, so a line a
// screen wrote whole is not cut where the window ends. A row's cursor, and the
// ask it belongs to, stay with its first line. A line that fits is untouched,
// and so is a line of an ask, which its screen wraps whole.
func (m *Model) wrapLong(pg page) page {
	var out []pline
	for _, l := range pg.lines {
		w := max(1, m.width-2)
		if l.box != boxNone {
			w = m.innerWidth()
		}
		if l.box == boxTop || l.box == boxBottom || l.ask != "" || width(l.text) <= w {
			out = append(out, l)
			continue
		}
		indent := 0
		for indent < len(l.text) && l.text[indent] == ' ' {
			indent++
		}
		lead := min(indent+2, w/2)
		rows := wrapRows
		if l.ref != nil && (l.ref.kind == rowAsk || l.ref.kind == rowQuestion) {
			// What a person decides on is never cut.
			rows = 1 << 20
		}
		// Every row, the first too, is wrapped to the room the continuation rows
		// have under their indent, so none is wider than the line may be.
		lines := wrapCell(l.text[indent:], max(1, w-lead), rows)
		for i, text := range lines {
			next := l
			next.text = spaces(indent) + text
			if i > 0 {
				next.ref, next.cont = nil, true
				next.text = spaces(lead) + text
			}
			out = append(out, next)
		}
	}
	pg.lines = out
	return pg
}

// rawPage builds the body of the screen on top, as its screen wrote it.
func (m *Model) rawPage() page {
	m.pageBuilds++
	e := m.top()
	switch e.screen {
	case screenTicket:
		return m.ticketPage(e.ticket)
	case screenBuild:
		return m.buildPage(e.ticket)
	case screenReview:
		return m.reviewPage(e.ticket)
	case screenSlice:
		return m.slicePage(e.ticket, e.slice)
	case screenBrief:
		return m.briefPage(e.ticket)
	case screenPublish:
		return m.publishPage(e.ticket)
	case screenLand:
		return m.landPage(e.ticket)
	case screenChart:
		return m.chartPage(e.chart)
	}
	return m.mainPage()
}

// minBodyRows is the rows a screen's body keeps whatever the panel and the
// footer ask for, and headerRows the rows over it: the title and its rule.
const (
	minBodyRows = 6
	headerRows  = 2
)

// View draws the screen: a header, the body windowed so the cursor shows,
// what the last command printed, and the keys, in exactly the height the
// terminal has.
func (m *Model) View() string {
	w := m.width
	m.followSel()
	l := m.layout()
	m.settle(&l)

	var tailPane []string
	switch {
	case l.tailH > 0:
		tailPane = append([]string{"  " + m.st.dim("tail "+strings.Repeat("─", max(0, w-7)))}, m.tailLines(w, l.tailH)...)
	case l.tailNote != "":
		tailPane = []string{fit("  "+m.st.dim(l.tailNote), w)}
	}

	start := m.windowStart(l.pg, l.sel, l.pageH, true)
	above := ""
	if m.counting(start) {
		// The count of what came in above takes the place of the mark that says
		// there is more above.
		above = m.newRow(m.top().fresh)
	}
	body, from, to := m.windowRows(l.pg, l.sel, start, l.pageH, w, above)
	m.drawn = drawnAsks(l.pg, from, to-from)
	m.rest(l, start)
	for len(body) < l.pageH {
		body = append(body, "")
	}
	out := append(append(append(append(l.head, body...), tailPane...), l.panel...), l.foot...)
	for i, line := range out {
		out[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(out, "\n")
}

// screenLayout is how the screen on top is laid out at the size the terminal
// has: its page and the row the cursor stands on, the rows around the body, and
// how many rows of the body the page has and the tail takes.
type screenLayout struct {
	pg                page
	sel               int
	head, foot, panel []string
	pageH, tailH      int
	// tailNote is the line that stands where the tail would be when the body is
	// too short for it, and takes a row of the body.
	tailNote string
}

// layout lays the screen on top out as View draws it.
func (m *Model) layout() screenLayout {
	w, h := m.width, m.height
	pg := m.page()
	if sub := m.subjectOf(pg); sub != m.keysOn {
		// The page was sized by a hint line cut for another subject: it is
		// built again, once, for this one.
		m.keysOn = sub
		pg = m.page()
	}
	refs := pg.refs()
	sel := m.top().sel
	if sel >= len(refs) {
		sel = len(refs) - 1
	}
	if sel < 0 {
		sel = 0
	}

	head := m.header(w)
	foot := m.footer(w)
	panel := m.panel(w)
	bodyH := h - len(head) - len(foot) - len(panel)
	if bodyH < minBodyRows && len(panel) > 0 {
		// The last command goes first: the notice says what to do.
		panel = panel[min(len(panel), minBodyRows-bodyH):]
		bodyH = h - len(head) - len(foot) - len(panel)
	}
	if bodyH < 1 {
		bodyH = 1
	}

	l := screenLayout{pg: pg, sel: sel, head: head, foot: foot, panel: panel, pageH: bodyH}
	switch {
	case m.tail && bodyH >= tailMinRows:
		l.tailH = bodyH / 2
		l.pageH = bodyH - l.tailH - 1
	case m.tail && bodyH >= 2:
		l.tailNote = tailNeeds(bodyH)
		l.pageH = bodyH - 1
	}
	return l
}

// tailMinRows is the body rows a screen needs to have its tail.
const tailMinRows = 8

// tailNeeds is the line that stands in the tail's place on a screen too short
// for it: what it needs and what the screen has.
func tailNeeds(rows int) string {
	return fmt.Sprintf("the tail needs %d body rows, this screen has %d", tailMinRows, rows)
}

// What View drew of an ask: some of its lines, or all of them. An ask the page
// has no line of is not in the record.
const (
	askCut = iota + 1
	askWhole
)

// drawnAsks is what the window of h rows from start draws of each ask the page
// has lines of. View records it as it draws, so a key acts on the screen the
// person read, notice and tail and all, and the one rule for acting on what a
// person must read first names no screen.
func drawnAsks(pg page, start, h int) map[string]int {
	total, shown := map[string]int{}, map[string]int{}
	for i, line := range pg.lines {
		if line.ask == "" {
			continue
		}
		total[line.ask]++
		if i >= start && i < start+h {
			shown[line.ask]++
		}
	}
	out := make(map[string]int, len(total))
	for id, n := range total {
		out[id] = askCut
		if shown[id] == n {
			out[id] = askWhole
		}
	}
	return out
}

// bodyRows is how many rows the screen's body has: the height less the
// header, the keys and what the last command printed, and less the tail when
// it is open.
func (m *Model) bodyRows() int {
	h := m.height - 2 - len(m.footer(m.width)) - len(m.panel(m.width))
	switch {
	case m.tail && h >= tailMinRows:
		h = h - h/2 - 1
	case m.tail && h >= 2:
		h--
	}
	return h
}

// windowStart is the first page line a window of h rows shows with the sel-th
// selectable row in view, then moved up by what the page is held above it
// (lead). With clamp, lead is cut to the lines there are above the window, so a
// page opened at its top lessens what is held as it is read down.
func (m *Model) windowStart(pg page, sel, h int, clamp bool) int {
	selLine, n := 0, 0
	for i, l := range pg.lines {
		if l.ref == nil {
			continue
		}
		if n == sel {
			selLine = i
		}
		n++
	}
	start := 0
	if len(pg.lines) > h {
		start = selLine - h/2
		start = max(0, min(start, lastStart(len(pg.lines), h)))
		if selLine < h {
			// A row in the first screenful leaves the page where it begins.
			start = 0
		}
		if n > 0 && sel == n-1 {
			// On the last row, show as much of what follows it as fits
			// with the row still in view: a panel of activity is below.
			start = max(0, min(selLine, lastStart(len(pg.lines), h)))
		}
		if n == 0 {
			// A page with no row to stand on is moved by its own offset.
			start = max(0, min(m.top().scroll, lastStart(len(pg.lines), h)))
		}
	}
	if n > 0 {
		lead := m.top().lead
		if clamp {
			lead = min(lead, start)
			m.top().lead = lead
		}
		start = max(0, start-lead)
	}
	return start
}

// windowSpan is the page lines the window of h rows from start draws, from the
// first to the one before to, once the marks and edges that keep a box whole
// have taken their rows. start is moved to the first line of a row, and to a
// box's top edge when only that would leave above it. With keep, the cursor's
// row stays in the span, which moves down as the marks cost it rows; without
// it, a row the page holds out of view stays out.
func windowSpan(pg page, sel, start, h int, keep bool) (from, to int, plain bool) {
	lines := pg.lines
	n := len(lines)
	if n <= h {
		return 0, n, false
	}
	selLine, k := -1, 0
	for i, l := range lines {
		if l.ref == nil {
			continue
		}
		if k == sel {
			selLine = i
		}
		k++
	}
	s := max(0, min(start, n-1))
	for s > 0 && lines[s].cont {
		s--
	}
	if s > 0 && lines[s].box == boxMid && lines[s-1].box == boxTop {
		s--
	}
	for {
		e := fitEnd(lines, s, h, false)
		if e == s {
			// A row taller than the window is split rather than cut off from its box.
			e = fitEnd(lines, s, h, true)
		}
		if e == s {
			// The marks and edges leave no row for a line: the window is plain,
			// and starts where the cursor's row is in it.
			if keep && selLine >= 0 {
				if selLine < s || selLine >= s+h {
					// The window starts at the cursor's row, which begins on a
					// line of its own.
					s = selLine
				}
			}
			return s, min(n, s+h), true
		}
		if !keep || selLine < 0 || selLine < e || e >= n {
			return s, e, false
		}
		// The window moves down a row, and stops at the cursor's row, which is the
		// first line of a row of its own: there e is past it.
		s++
		for s < n-1 && lines[s].cont {
			s++
		}
	}
}

// boxOf is the positions of the top edge and of the bottom edge of the box that
// line i is in, or -1 for a line that is in none.
func boxOf(lines []pline, i int) (top, bottom int) {
	if lines[i].box == boxNone {
		return -1, -1
	}
	top, bottom = i, i
	for top > 0 && lines[top].box != boxTop {
		top--
	}
	for bottom < len(lines)-1 && lines[bottom].box != boxBottom {
		bottom++
	}
	return top, bottom
}

// fitEnd is the end of the window of h rows that starts at line s: the most
// lines it can draw once the rows of the marks and edges are counted. A window
// that does not begin at the page's top starts with a mark of what is above, or
// with the top edge of the box it is in and a mark inside it; one that does not
// reach the page's end finishes with a mark of what is below, or with a mark
// inside the box it is in and that box's bottom edge. A row is not split from its
// continuation rows unless split says it may be, for a row taller than the window.
func fitEnd(lines []pline, s, h int, split bool) int {
	n := len(lines)
	top := 0
	if s > 0 {
		top = 1
		if lines[s].box != boxNone && lines[s].box != boxTop {
			top = 2
		}
	}
	for e := min(n, s+h); e > s; e-- {
		if e < n && lines[e].cont && !split {
			continue
		}
		bottom := 0
		if e < n {
			bottom = 1
			if lines[e-1].box != boxNone && lines[e-1].box != boxBottom {
				if lines[e].box == boxBottom {
					continue
				}
				bottom = 2
			}
		}
		if (e-s)+top+bottom <= h {
			return e
		}
	}
	return s
}

// plainRows are the page lines from..to as rows with no marks and no edges, for
// a window too short to give a row to them.
func (m *Model) plainRows(pg page, sel, from, to, w int) []string {
	var rows []string
	idx := 0
	for i, l := range pg.lines {
		marker := "  "
		if l.ref != nil {
			if idx == sel {
				marker = m.st.key("▸") + " "
			}
			idx++
		}
		if i < from || i >= to {
			continue
		}
		if l.box != boxNone {
			rows = append(rows, fit(m.frameLine(l, marker, w), w))
			continue
		}
		rows = append(rows, fit(marker+l.text, w))
	}
	return rows
}

// windowRows renders the window of h rows from start and the span of page
// lines it drew. The window works on boxes and loose lines: a box that fits is
// drawn whole, and one that does not is drawn with its top edge, the rows that
// fit, a dim mark inside it for each side that is cut and its bottom edge. A
// window that does not show the page's first line starts with a dim "↑ N more"
// (above, when it is given, says it instead), and one that does not show the
// last ends with "↓ N more". A row is never cut from its continuation rows.
func (m *Model) windowRows(pg page, sel, start, h, w int, above string) (rows []string, from, to int) {
	lines := pg.lines
	from, to, plain := windowSpan(pg, sel, start, h, above != "" || m.top().lead == 0)
	if plain {
		return m.plainRows(pg, sel, from, to, w), from, to
	}
	mark := func(text string, boxed bool) string {
		l := pline{text: m.st.dim(text), box: boxNone}
		if boxed {
			l.box = boxMid
			return fit(m.frameLine(l, "  ", w), w)
		}
		return fit("  "+l.text, w)
	}
	if from > 0 {
		upText := fmt.Sprintf("↑ %d more", from)
		if top, _ := boxOf(lines, from); top >= 0 && lines[from].box != boxTop {
			rows = append(rows, fit(m.frameLine(lines[top], "  ", w), w))
			if above != "" {
				rows = append(rows, fit(m.frameLine(pline{text: above, box: boxMid}, "  ", w), w))
			} else {
				rows = append(rows, mark(upText, true))
			}
		} else if above != "" {
			rows = append(rows, fit("  "+above, w))
		} else {
			rows = append(rows, mark(upText, false))
		}
	}
	idx := 0
	for i, l := range lines {
		marker := "  "
		if l.ref != nil {
			if idx == sel {
				marker = m.st.key("▸") + " "
			}
			idx++
		}
		if i < from || i >= to {
			continue
		}
		if l.box != boxNone {
			rows = append(rows, fit(m.frameLine(l, marker, w), w))
			continue
		}
		rows = append(rows, fit(marker+l.text, w))
	}
	if to < len(lines) {
		if _, bottom := boxOf(lines, to-1); bottom >= to && lines[to-1].box != boxBottom {
			rows = append(rows, mark(fmt.Sprintf("↓ %d more", len(lines)-to), true))
			rows = append(rows, fit(m.frameLine(lines[bottom], "  ", w), w))
		} else {
			rows = append(rows, mark(fmt.Sprintf("↓ %d more", len(lines)-to), false))
		}
	}
	return rows, from, to
}

// header is the title line and the rule under it.
func (m *Model) header(w int) []string {
	e := m.top()
	crumbs := m.st.bold("jig")
	if m.cfg.Project != "" {
		crumbs += "  " + m.cfg.Project
	}
	var right, title, idCrumb string
	switch e.screen {
	case screenMain:
		if m.readingStore() {
			break
		}
		n := len(m.w.order)
		right = strconv.Itoa(n) + " " + plural(n, "ticket", "tickets")
		if nc := len(m.w.chartOrder); nc > 0 {
			right += " · " + strconv.Itoa(nc) + " " + plural(nc, "chart", "charts")
		}
		right = m.st.dim(right)
	case screenChart:
		crumbs += " › " + m.st.bold(e.chart)
		if c := m.w.charts[e.chart]; c != nil {
			if title := c.title(); title != "" && title != e.chart {
				crumbs += "  " + title
			}
			state, label := m.chartStatus(c, m.chartRows(c))
			right = m.st.icon(state) + " " + m.st.state(state, label)
		}
	default:
		t := m.w.tickets[e.ticket]
		crumbs += " › " + m.st.bold(e.ticket)
		idCrumb = crumbs
		if t != nil && t.title != "" {
			title = t.title
		}
		if t != nil && e.screen == screenTicket {
			if k := m.kindIn(t); k != "" {
				crumbs += "  " + m.st.dim(k)
			}
		}
		switch e.screen {
		case screenBrief:
			crumbs += " › 1 brief"
		case screenBuild:
			crumbs += " › 2 build"
		case screenReview:
			crumbs += " › 3 review"
		case screenPublish:
			crumbs += " › 4 publish"
		case screenLand:
			crumbs += " › 5 land"
		case screenSlice:
			crumbs += " › 2 build › slice " + m.st.bold(e.slice)
		}
		if t != nil {
			state, label := m.stepLabel(t)
			if isMulti(t) {
				// A multi-task has no phase: its word is where its own pull request is.
				state, label = m.multiLabel(t)
			}
			if t.awaiting {
				state, label = StateQueued, "waiting"
			}
			if e.screen == screenSlice {
				for _, s := range t.slices(m.zone) {
					if s.ID == e.slice {
						state, label = s.State, s.State
					}
				}
			}
			right = m.st.icon(state) + " " + m.st.state(state, label)
		}
	}
	if title != "" {
		// The title gives way first: it takes what the id, the step and the
		// state leave, whole or cut, and none when nothing is left.
		if room := w - width(crumbs) - width(right) - 3; room > 1 {
			crumbs = idCrumb + "  " + fit(title, room) + strings.TrimPrefix(crumbs, idCrumb)
		}
	}
	gap := w - width(crumbs) - width(right)
	if gap < 1 {
		crumbs = fit(crumbs, max(1, w-width(right)-1))
		gap = max(1, w-width(crumbs)-width(right))
	}
	return []string{crumbs + spaces(gap) + right, m.st.dim(strings.Repeat("─", w))}
}

// footer is the rule, then the line being typed, the question being asked, or
// the keys that do something here.
func (m *Model) footer(w int) []string {
	rule := m.st.dim(strings.Repeat("─", w))
	switch {
	case m.input != nil:
		out := []string{rule}
		out = append(out, fit("  "+m.st.dim(m.input.head), w))
		for _, l := range wrapWhole(m.input.about, w-2) {
			out = append(out, fit("  "+m.st.dim(l), w))
		}
		out = append(out, fit("  "+m.st.key(m.input.prompt+" ›")+" "+string(m.input.value)+m.st.dim("▏"), w))
		return append(out, m.hintLines(w, "enter", "send", "esc", "cancel")...)
	case m.ask != nil:
		out := []string{rule, fit("  "+m.st.state(StateFindings, m.ask.prompt), w)}
		return append(out, m.hintLines(w, "y", "yes", "any other key", "no")...)
	}
	return append([]string{rule}, m.hintLines(w, m.keyHints()...)...)
}

// hintLines lays key/meaning pairs out in as many lines as w cells need.
func (m *Model) hintLines(w int, pairs ...string) []string {
	sep := m.st.dim(" · ")
	var lines []string
	cur := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		hint := m.st.key(pairs[i]) + " " + m.st.dim(pairs[i+1])
		if cur != "" && width(cur)+width(sep)+width(hint) > w-2 {
			lines = append(lines, "  "+cur)
			cur = ""
		}
		if cur != "" {
			cur += sep
		}
		cur += hint
	}
	if cur != "" {
		lines = append(lines, "  "+cur)
	}
	return lines
}

// hint is a key on the hint line: the key, its meaning, and the action that
// decides whether it is listed (none for a key that is always there).
type hint struct {
	key, text string
	act       action
}

// keyHints are the keys that do something on the screen on top, as key and
// meaning pairs: the screen's keys, and of those the actions only the ones can
// lets run now. The key back and the key quit come first, so a line that wraps
// never strands them.
func (m *Model) keyHints() []string {
	sub := m.keysOn
	keys := m.screenKeys(sub)
	// The key up to the issue, on the screens of a task that is in one's plan, and
	// the one that shows the map, on the plan: each goes after the key quit, on
	// a screen that has a key back.
	var extra *hint
	switch up := m.upFrom(); {
	case up != "" && m.multiParent(up) != "":
		extra = &hint{key: "c", text: "multi-task"}
	case up != "" && len(m.issuesOf(up)) > 0:
		extra = &hint{key: "c", text: "issue"}
	case m.top().screen == screenChart:
		if c := m.w.charts[m.top().chart]; c != nil && c.issueID() != "" {
			extra = &hint{key: "m", text: "map"}
		}
	}
	flat := make([]string, 0, 2*len(keys)+2)
	for _, k := range keys {
		if k.act != "" {
			if ok, _ := m.canOn(sub, k.act); !ok {
				continue
			}
		}
		flat = append(flat, k.key, k.text)
		if k.key == "q" && extra != nil && keys[0].key == "esc" {
			flat = append(flat, extra.key, extra.text)
		}
	}
	return flat
}

// screenKeys are the keys the screen on top has, in the order they are listed,
// whether or not their action can run: the hint line lists the ones can lets
// run, and a key the screen does not have runs nothing.
func (m *Model) screenKeys(sub subject) []hint {
	id := sub.ticket
	move := hint{key: "↑↓", text: "move"}
	open := hint{key: "enter", text: "open"}
	back := hint{key: "esc", text: "back"}
	answer := hint{"a", "answer", actAnswer}
	keep := hint{"k", "keep", actKeep}
	dismiss := hint{"d", "dismiss", actDismiss}
	gate := hint{"g", "gate", actGate}
	publish := hint{"p", "publish", actPublish}
	requeue := hint{"u", "requeue", actRequeue}
	approve := hint{"v", "approve", actApprove}
	say := hint{"say", `"Land ` + id + `"`, actLandWord}
	land := hint{key: "5", text: "land"}
	tail := hint{key: "t", text: "tail"}
	quit := hint{key: "q", text: "quit"}
	var keys []hint
	switch m.top().screen {
	case screenMain:
		keys = []hint{quit, move, open, gate, publish, answer, say, tail, {key: "r", text: "refresh"}}
	case screenChart:
		keys = []hint{back, quit, move, {key: "enter", text: "open ticket"}, gate, publish, answer, say, tail, {key: "r", text: "refresh"}}
	case screenTicket:
		keys = []hint{back, quit, move, open, answer, keep, dismiss, requeue, approve, gate, publish, say, land, tail}
		if t := m.w.tickets[m.top().ticket]; t != nil && t.awaiting {
			// Nothing to move on or act on until the ticket exists.
			keys = []hint{back, quit, tail}
		} else if isMulti(t) {
			// A multi-task's tasks open; nothing else is done from its screen.
			keys = []hint{back, quit, move, open, tail}
		}
	case screenBrief:
		keys = []hint{back, quit, move, open, approve, say, land, tail}
	case screenBuild:
		keys = []hint{back, quit, move, open, requeue, gate, say, land, tail}
	case screenReview:
		keys = []hint{back, quit, move, keep, dismiss, gate, say, land, tail}
	case screenPublish:
		keys = []hint{back, quit, {key: "↑↓", text: "scroll"}, publish, say, land, tail}
	case screenSlice:
		keys = []hint{back, quit, move, answer, requeue, say, land, tail}
	case screenLand:
		keys = []hint{back, quit, {key: "↑↓", text: "scroll"}, say, tail}
	}
	return keys
}

// panel is the last command as one line (whole, for a verb that leaves the next
// move to someone else), and the notice, above the keys.
func (m *Model) panel(w int) []string {
	var out []string
	if m.last != nil {
		out = append(out, m.lastLines(m.last, w)...)
	}
	if m.notice != "" {
		// Drawn whole, in the rows it needs: what it tells the person is the
		// point of it, and a cut notice loses its end.
		for _, l := range wrapWhole(m.notice, w-2) {
			out = append(out, fit("  "+m.st.state(StateFindings, l), w))
		}
	}
	return out
}

// helpWrapRows is the most rows a verb's help line takes in the panel when it is
// shown whole: a sentence of the verb's own is never this long.
const helpWrapRows = 6

// lastLines are the panel's rows for a key's command: the one line, and for a
// verb that leaves the next move to someone else the help line it printed, whole
// and wrapped, beneath the command. The words are the verb's own; the TUI adds
// none. They are part of the line and expire with it.
func (m *Model) lastLines(o *outcome, w int) []string {
	if s := o.shown(); o.leavesTheMove() && s.Code == 0 {
		if help, ok := firstHelp(s.Out); ok {
			out := []string{fit("  "+m.st.icon(StateOK)+" "+m.st.bold(m.shownLine(s.Args)), w)}
			for _, l := range wrap(help, w-4, helpWrapRows) {
				out = append(out, "    "+m.st.dim(l))
			}
			return out
		}
	}
	return []string{m.lastLine(o, w)}
}

// firstHelp is the first line of the help a verb printed, false when it printed
// none.
func firstHelp(out string) (string, bool) {
	lines := outputLines(out)
	for i, l := range lines {
		if strings.HasPrefix(l, "help[") && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1]), true
		}
	}
	return "", false
}

// lastLine is a key's command as typed and what the verb said of it: its own
// first sentence when it worked, its error and code when it did not. The step
// that failed is the one shown, so a chain that stopped reads as the failure.
// The raw output is not printed, and a line too long for the screen is cut.
func (m *Model) lastLine(o *outcome, w int) string {
	s := o.shown()
	state, said := StateOK, firstSentence(s.Out)
	if s.Code != 0 {
		state, said = StateFail, errorSentence(s.Out)
	}
	line := "  " + m.st.icon(state) + " " + m.st.bold(m.shownLine(s.Args))
	if said != "" {
		if state == StateFail {
			said = m.st.state(StateFail, said)
		} else {
			said = m.st.dim(said)
		}
		line += "  " + said
	}
	return fit(line, w)
}

// firstSentence is what a verb that worked says of it: the first line of its
// help, which is a sentence, else its first line.
func firstSentence(out string) string {
	lines := outputLines(out)
	for i, l := range lines {
		if strings.HasPrefix(l, "help[") && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1])
		}
	}
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

// errorSentence is a refusal's error and its code, "error text (CODE)"; output
// that names neither says its first line.
func errorSentence(out string) string {
	var msg, code string
	for _, l := range outputLines(out) {
		switch {
		case msg == "" && strings.HasPrefix(l, "error:"):
			msg = strings.TrimSpace(strings.TrimPrefix(l, "error:"))
		case code == "" && strings.HasPrefix(l, "code:"):
			code = strings.TrimSpace(strings.TrimPrefix(l, "code:"))
		}
	}
	switch {
	case msg != "" && code != "":
		return msg + " (" + code + ")"
	case msg != "":
		return msg
	case code != "":
		return code
	}
	return firstSentence(out)
}

// outputLines are the lines a command printed, without blanks.
func outputLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimRight(l, " \t"))
		}
	}
	return lines
}

// tailLines are the last n lines of the stream the screen is about, one line
// each, newest first; a stream that does not fit ends with the row that counts
// the lines the screen kept and does not show.
func (m *Model) tailLines(w, n int) []string {
	var envs []events.Envelope
	e := m.top()
	if e.screen == screenMain {
		envs = m.w.feed
	} else if e.screen == screenChart {
		// Every line of the chart's tickets, as the feed names them.
		if c := m.w.charts[e.chart]; c != nil {
			for _, id := range c.ids() {
				if t := m.w.tickets[id]; t != nil {
					envs = append(append(envs, t.lines...), t.ledger...)
				}
			}
		}
	} else if t := m.w.tickets[e.ticket]; t != nil {
		envs = append(append(envs, t.lines...), t.ledger...)
		if e.screen == screenSlice {
			var only []events.Envelope
			for _, l := range envs {
				if l.Slice == e.slice {
					only = append(only, l)
				}
			}
			envs = only
		}
	}
	envs = newestFirst(envs, func(l events.Envelope) time.Time { at, _ := l.At(); return at })
	envs, older := cutNewest(envs, n)
	withTicket := e.screen == screenMain || e.screen == screenChart
	cells := make([][]string, len(envs))
	for i, l := range envs {
		cells[i] = m.tailCells(l, withTicket)
	}
	room := n
	if older > 0 {
		room--
	}
	out := make([]string, 0, n)
	for _, lines := range laidWithin(tailColumns, cells, max(1, w-2), room) {
		for _, l := range lines {
			out = append(out, fit("  "+l, w))
		}
	}
	if older > 0 {
		out = append(out, fit("  "+m.olderRow(older), w))
	}
	return out
}

// tailColumns are the columns of a tail line: the time and where it came from,
// the event, the ticket, the slice and what it is about.
var tailColumns = []column{{}, {gap: 1}, {gap: 1}, {gap: 1}, {free: true, gap: 1}}

// tailCells are one stream line as cells: the time, where it came from, the
// event, the ticket when the screen is about several, the slice, and what the
// line is about.
func (m *Model) tailCells(l events.Envelope, withTicket bool) []string {
	src := "j"
	if l.Src == events.SrcLedger {
		src = "l"
	}
	event := l.Event
	if s := eventState(l); s != "" {
		event = m.st.state(s, event)
	}
	ticket := ""
	if withTicket {
		ticket = l.Ticket
	}
	return []string{m.st.dim(clock(l, m.zone) + " " + src), event, ticket, l.Slice, detailOf(l)}
}

// detailOf is what a stream line is about, beyond its event: whatever of the
// line's own fields name a subject or say what came of it.
func detailOf(l events.Envelope) string {
	var parts []string
	for _, s := range []string{l.Tool, l.Ref, l.Outcome, short(l.Commit), l.Summary, prRef(l.URL)} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if l.Provenance != "" {
		parts = append(parts, "("+l.Provenance+")")
	}
	if l.Session != "" {
		parts = append(parts, l.Session)
	}
	return strings.Join(parts, " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// lastStart is the furthest down a window of h rows can begin on a page of n
// lines: the window that does not begin at the top gives a row to the mark that
// says so, so it is one line further than a plain one.
func lastStart(n, h int) int { return max(0, n-h+1) }

// taken is how many lines p has once the text too long for the screen is wrapped,
// which is what the panels below it have to size themselves against.
func (m *Model) taken(p *page) int {
	*p = m.wrapLong(*p)
	return len(p.lines)
}
