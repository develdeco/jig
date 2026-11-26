package tui

import (
	"slices"
)

// A screen is a set of panes, and one of them has the focus. A pane is a panel
// of the screen, named by its title: one whose rows open something takes a
// cursor, one of text only takes none and scrolls by lines. The cursor, the
// keys that move it and the row every key acts on are the focused pane's;
// every other pane keeps the row its cursor was left on, found again by the
// row's identity. The page keeps one index, navEntry.sel, among the rows of the
// whole page: it is always the focused pane's cursor, so the window, the hold
// and the click work from it unchanged.

// pane is a panel of a page: the lines from its top edge to its bottom edge, and
// the rows the cursor can stand on inside it, which are rows first to
// first+rows-1 of the page.
type pane struct {
	title       string
	top, bottom int
	first, rows int
	// home is the place among the pane's rows of the row its cursor begins on,
	// -1 when the pane gives none.
	home int
}

// panes are the panels of the page, in the order they are drawn. A row outside
// a panel is in no pane.
func (p page) panes() []pane {
	var out []pane
	row := 0
	for i, l := range p.lines {
		switch {
		case l.box == boxTop:
			out = append(out, pane{title: l.text, top: i, bottom: i, first: row, home: -1})
		case l.box != boxNone && len(out) > 0:
			cur := &out[len(out)-1]
			cur.bottom = i
			if l.ref != nil {
				if l.home {
					cur.home = cur.rows
				}
				cur.rows++
			}
		}
		if l.ref != nil {
			row++
		}
	}
	return out
}

// paneOfRow is the place among panes of the pane that holds the sel-th row of the
// page, -1 when none does.
func paneOfRow(ps []pane, sel int) int {
	for i, p := range ps {
		if sel >= p.first && sel < p.first+p.rows {
			return i
		}
	}
	return -1
}

// paneCursor is what a pane keeps while the focus is elsewhere: the identity of
// its cursor's row and its place in the pane (for a row that went away), and for
// a pane of text how far it is scrolled.
type paneCursor struct {
	id     rowIdentity
	idx    int
	set    bool
	scroll int
}

// focusedPane is the title of the pane that has the focus on the screen on top,
// "" when the screen has none.
func (m *Model) focusedPane() string {
	if len(m.panesNow) == 0 {
		return ""
	}
	return m.top().paneFocus
}

// syncPanes makes the screen's focus and the cursor of its focused pane agree
// with the page pg: a cursor put on a row by anything but this (a move, a click,
// the follow of the current step) takes its pane for the focus; otherwise the
// focused pane's cursor is found again by its row's identity, so a row that came
// in above it does not move it; a focus whose pane went away goes to the first
// pane. The project's list is one pane and keeps its own cursor.
func (m *Model) syncPanes(pg page) {
	e := m.top()
	if e.screen == screenMain {
		m.panesNow, e.textFocus = nil, false
		return
	}
	ps := pg.panes()
	// A new slice each time: a subject keeps the one it was given.
	m.panesNow = make([]string, 0, len(ps))
	for _, p := range ps {
		m.panesNow = append(m.panesNow, p.title)
	}
	if len(pg.allPanes) > 0 {
		// A zoomed page has the one pane; the focus still moves over them all.
		m.panesNow = pg.allPanes
	}
	if len(ps) == 0 {
		e.textFocus = false
		return
	}
	refs := pg.refs()
	fi := -1
	for i, p := range ps {
		if p.title == e.paneFocus {
			fi = i
			break
		}
	}
	adopted := false
	if !e.seenSet || e.sel != e.seenSel {
		// Something put the cursor on a row: the pane that has it has the focus.
		if sel := min(max(e.sel, 0), len(refs)-1); sel >= 0 {
			if i := paneOfRow(ps, sel); i >= 0 {
				e.sel = sel
				fi, adopted = i, true
			}
		}
	}
	if fi < 0 {
		// A screen's first draw puts the focus on the first pane with rows; a
		// pane that went away hands it to the first pane.
		fi = 0
		if e.paneFocus == "" {
			for i, p := range ps {
				if p.rows > 0 {
					fi = i
					break
				}
			}
		}
		m.moveFocus(pg, ps, refs, fi, false)
		return
	}
	if ps[fi].title != e.paneFocus {
		m.moveFocus(pg, ps, refs, fi, adopted)
		return
	}
	p := ps[fi]
	if p.rows == 0 {
		e.textFocus = true
		e.seenSel, e.seenSet = e.sel, true
		return
	}
	e.textFocus = false
	pc := e.paneCurs[p.title]
	if !adopted && pc.set {
		e.sel = p.first + pc.find(p, refs, pg.windowed[p.title])
	}
	e.rememberPane(pg, p, refs)
	e.seenSel, e.seenSet = e.sel, true
}

// find is the place in pane p, among its rows, that the cursor c names: the row
// of its identity, else the place it was at, kept inside the pane.
//
// A pane that draws only a window of its rows (full holds all of them) keeps its
// place among all of them, and the row it falls on is the one the window was
// drawn around.
func (c paneCursor) find(p pane, refs []rowRef, full []rowRef) int {
	id := c.id
	if len(full) > 0 {
		at := slices.IndexFunc(full, func(r rowRef) bool { return r.identity() == id })
		if at < 0 {
			at = min(max(c.idx, 0), len(full)-1)
		}
		id = full[at].identity()
	}
	for i := 0; i < p.rows; i++ {
		if refs[p.first+i].identity() == id {
			return i
		}
	}
	return min(max(c.idx, 0), p.rows-1)
}

// rememberPane notes the row the cursor stands on as the pane's own. Its place
// is among all the pane's rows when the pane draws only a window of them.
func (e *navEntry) rememberPane(pg page, p pane, refs []rowRef) {
	if p.rows == 0 || e.sel < p.first || e.sel >= p.first+p.rows {
		return
	}
	if e.paneCurs == nil {
		e.paneCurs = map[string]paneCursor{}
	}
	pc := e.paneCurs[p.title]
	pc.id, pc.idx, pc.set = refs[e.sel].identity(), e.sel-p.first, true
	if full := pg.windowed[p.title]; len(full) > 0 {
		for i, r := range full {
			if r.identity() == pc.id {
				pc.idx = i
				break
			}
		}
	}
	e.paneCurs[p.title] = pc
}

// paneCursorIn is the place, among ids, of the row the pane titled title keeps
// its cursor on: by identity, else the place it was at, else def. The builder
// of a pane that draws only a window of its rows reads it to draw the window
// around the cursor.
func (m *Model) paneCursorIn(title string, ids []rowIdentity, def int) int {
	pc := m.top().paneCurs[title]
	if !pc.set || len(ids) == 0 {
		return min(max(def, 0), max(len(ids)-1, 0))
	}
	if i := slices.Index(ids, pc.id); i >= 0 {
		return i
	}
	return min(max(pc.idx, 0), len(ids)-1)
}

// markLines is how many lines the counts of the rows above and below a window of
// rows from to to-1 of n take: one each, or one for both when merged.
func markLines(from, to, n int, merged bool) int {
	k := 0
	if from > 0 {
		k++
	}
	if to < n {
		k++
	}
	if merged {
		k = 1
	}
	return k
}

// windowAround grows the window of rows f to e-1 of n, which holds the cursor,
// row by row while the lines it takes stay within room: the rows, extra lines
// more than one each, and the counts of what is left out above and below, one
// line each, or one for both when there is no room for two. It says whether
// the window needs the one line.
func windowAround(n, f, e, extra, room int) (from, to int, merged bool) {
	cost := func(f, e int) int {
		k := e - f + extra
		return k + markLines(f, e, n, f > 0 && e < n && k+2 > room)
	}
	e = min(e, n)
	f = min(f, e)
	for {
		switch {
		case e < n && cost(f, e+1) <= room:
			e++
		case f > 0 && cost(f-1, e) <= room:
			f--
		default:
			return f, e, f > 0 && e < n && e-f+extra+2 > room
		}
	}
}

// windowCost is how many lines the window of rows from to to-1 takes with extra
// lines more than one each, the counts included.
func windowCost(n, from, to, extra, room int) int {
	k := to - from + extra
	return k + markLines(from, to, n, from > 0 && to < n && k+2 > room)
}

// moveWindowed is a move in a pane that draws only a window of its rows: the
// cursor goes to the row delta places on among all of them, which the next page
// draws a window around. It says whether the focused pane is one.
func (m *Model) moveWindowed(pg page, delta int) bool {
	e := m.top()
	full := pg.windowed[e.paneFocus]
	if len(full) == 0 {
		return false
	}
	ids := make([]rowIdentity, len(full))
	for i, r := range full {
		ids[i] = r.identity()
	}
	at := m.paneCursorIn(e.paneFocus, ids, 0)
	to := min(max(at+delta, 0), len(full)-1)
	switch {
	case delta <= -(1 << 19):
		to = 0
	case delta >= 1<<19:
		to = len(full) - 1
	}
	pc := e.paneCurs[e.paneFocus]
	pc.id, pc.idx, pc.set = ids[to], to, true
	e.paneCurs[e.paneFocus] = pc
	e.follow, e.lead = false, 0
	if delta <= -(1 << 19) {
		// Home goes to the page's top when the pane's first row is the page's.
		for _, p := range pg.panes() {
			if p.title == e.paneFocus && p.first == 0 {
				e.lead = 1 << 20
			}
		}
	}
	return true
}

// moveFocus gives the focus to pane i: the pane it leaves keeps its cursor and
// its scroll, and the screen's cursor is the pane's own, which is its first row
// the first time. A cursor that was following the current step stays where it
// stands. keep says the screen's cursor is already on the pane's row.
func (m *Model) moveFocus(pg page, ps []pane, refs []rowRef, i int, keep bool) {
	e := m.top()
	moving := e.paneFocus != ""
	if old := e.paneFocus; old != "" {
		if e.paneCurs == nil {
			e.paneCurs = map[string]paneCursor{}
		}
		pc := e.paneCurs[old]
		if e.textFocus {
			pc.scroll, pc.set = e.scroll, true
		}
		e.paneCurs[old] = pc
	}
	p := ps[i]
	if e.paneFocus != "" && e.paneFocus != p.title {
		e.follow, e.lead = false, 0
	}
	e.paneFocus = p.title
	if !moving && e.screen != screenTicket && e.screen != screenChart {
		// A step's screen opens at its top, so what is above its first row is read
		// before the rows are: the first move reads the page a line at a time.
		e.lead = 1 << 20
	}
	if p.rows == 0 {
		// A pane of text is read from where it was left, the first time from its
		// top edge.
		e.textFocus = true
		if pc := e.paneCurs[p.title]; pc.set {
			e.scroll = pc.scroll
		} else if moving {
			e.scroll = clampStart(pg.lines, m.bodyRows(), p.top)
		}
	} else {
		e.textFocus = false
		if !keep {
			pc := e.paneCurs[p.title]
			switch {
			case pc.set:
				e.sel = p.first + pc.find(p, refs, pg.windowed[p.title])
			case p.home >= 0:
				e.sel = p.first + p.home
			default:
				e.sel = p.first
			}
		}
		e.rememberPane(pg, p, refs)
	}
	e.seenSel, e.seenSet = e.sel, true
}

// switchPane is `tab` and `shift+tab`: the focus goes delta panes on, wrapping.
// A zoomed screen moves the zoom with the focus.
func (m *Model) switchPane(delta int) {
	e := m.top()
	zoomed := e.zoom != ""
	if zoomed {
		m.syncPanes(m.page())
		m.setZoom("")
	}
	pg := m.page()
	m.syncPanes(pg)
	ps := pg.panes()
	if len(ps) < 2 {
		if zoomed {
			m.setZoom(e.paneFocus)
		}
		return
	}
	i := slices.IndexFunc(ps, func(p pane) bool { return p.title == m.top().paneFocus })
	if i < 0 {
		i = 0
	}
	m.moveFocus(pg, ps, pg.refs(), ((i+delta)%len(ps)+len(ps))%len(ps), false)
	if zoomed {
		m.setZoom(e.paneFocus)
	}
}

// focusPane gives the focus to the pane titled title, when the screen has it.
func (m *Model) focusPane(title string) {
	pg := m.page()
	m.syncPanes(pg)
	ps := pg.panes()
	if i := slices.IndexFunc(ps, func(p pane) bool { return p.title == title }); i >= 0 && title != m.top().paneFocus {
		m.moveFocus(pg, ps, pg.refs(), i, false)
	}
}

// paneSpan is the rows of the focused pane: the place among the page's rows of
// its first, and how many it has. A screen with no pane, or a focus on no pane,
// is one span of every row.
func (m *Model) paneSpan(pg page) (first, n int) {
	refs := 0
	for _, l := range pg.lines {
		if l.ref != nil {
			refs++
		}
	}
	e := m.top()
	if e.screen == screenMain {
		return 0, refs
	}
	for _, p := range pg.panes() {
		if p.title == e.paneFocus && p.rows > 0 {
			return p.first, p.rows
		}
	}
	return 0, refs
}

// paneMarks are the rows the unfocused panes' cursors stand on, and the panes
// that have the focus lit, set on the page lines by layout.
func (m *Model) paneMarks(pg *page) {
	e := m.top()
	if e.screen == screenMain {
		return
	}
	ps := pg.panes()
	refs := pg.refs()
	for _, p := range ps {
		focused := p.title == e.paneFocus
		if focused {
			for i := p.top; i <= p.bottom; i++ {
				pg.lines[i].focus = true
			}
			continue
		}
		if pc := e.paneCurs[p.title]; pc.set && p.rows > 0 {
			pg.marks = append(pg.marks, p.first+pc.find(p, refs, pg.windowed[p.title]))
		}
	}
}
