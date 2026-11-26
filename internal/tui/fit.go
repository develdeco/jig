package tui

import (
	"fmt"
	"sort"
	"strconv"
)

// What the person expanded never changes the height of a page. The page is laid
// out folded, and the pane that holds the cursor's expanded row is drawn into
// exactly the lines it had folded: the rows are windowed around the cursor, and a
// cursor row taller than what is left is read by lines, with a count above and
// below it, each kept only while a line of the row is left beside it.

// tallInfo is what the last page drew of a row it read by lines: its identity,
// how many lines it has, how many of them are drawn and how far it can be
// scrolled.
type tallInfo struct {
	id    rowIdentity
	total int
	part  int
	max   int
}

// maxScroll is the most lines of the row that can be above the part drawn.
func (t tallInfo) maxScroll() int { return t.max }

// group is a row of a pane as it is drawn: its lines, the first of which carries
// the row's place, or a count of rows left out.
type group struct {
	lines []pline
	id    rowIdentity
	row   bool
}

// groupsOf are the lines of a pane's body split into rows: a line with a ref, or
// with the rows it counts, or that no row holds, starts one; a continuation line
// and a line under a row belong to the row above.
func groupsOf(lines []pline) []group {
	var out []group
	for _, l := range lines {
		start := l.ref != nil || len(l.hiddenRefs) > 0 || (!l.cont && !l.under) || len(out) == 0
		if start {
			g := group{}
			if l.ref != nil {
				g.id, g.row = l.ref.identity(), true
			}
			out = append(out, g)
		}
		out[len(out)-1].lines = append(out[len(out)-1].lines, l)
	}
	return out
}

// refsOfGroups are the rows the groups stand for, in order.
func refsOfGroups(gs []group) []rowRef {
	var out []rowRef
	for _, g := range gs {
		for _, l := range g.lines {
			if l.ref != nil {
				out = append(out, *l.ref)
			}
			out = append(out, l.hiddenRefs...)
		}
	}
	return out
}

// cursorID is the identity of the row the screen's cursor stands on, false when
// it stands on none.
func (m *Model) cursorID() (rowIdentity, string, bool) {
	e := m.top()
	if e.screen == screenMain {
		if !e.curSet {
			return rowIdentity{}, "", false
		}
		return e.cur, "", true
	}
	pc := e.paneCurs[e.paneFocus]
	if e.textFocus || !pc.set {
		return rowIdentity{}, "", false
	}
	return pc.id, e.paneFocus, true
}

// fitPane is pg, the page with what is expanded laid out, as the page the person
// sees: folded, but for each pane that holds an expanded row, which takes the
// lines it had folded, whatever pane has the focus. A page that holds an
// expanded pane of text is the one laid out whole: such a pane is read by the
// page.
func (m *Model) fitPane(pg, folded page) page {
	e := m.top()
	curID, curPane, curOK := m.cursorID()
	var keys []expKey
	for k := range e.expanded {
		if k.id == (rowIdentity{}) {
			return pg
		}
		keys = append(keys, k)
	}
	live := func(k expKey) bool {
		return curOK && k.id == curID && (e.screen == screenMain || k.pane == curPane)
	}
	sort.Slice(keys, func(a, b int) bool {
		if la, lb := live(keys[a]), live(keys[b]); la != lb {
			return la
		}
		if keys[a].pane != keys[b].pane {
			return keys[a].pane < keys[b].pane
		}
		return keys[a].id.id < keys[b].id.id
	})
	// The pane the cursor is in draws the cursor's row: a row expanded earlier in
	// that pane is folded while the cursor is elsewhere in it.
	cursorPane := curPane
	if curOK && e.screen == screenMain {
		for i, l := range folded.lines {
			if l.ref != nil && l.ref.identity() == curID {
				if p := paneAround(folded.lines, i); p.top >= 0 {
					cursorPane = folded.lines[p.top].text
				}
				break
			}
		}
	}
	out, fitted := folded, map[string]bool{}
	windowed := false
	for _, k := range keys {
		if fitted[k.pane] || (curOK && k.pane == cursorPane && !live(k)) {
			continue
		}
		if next, w, ok := m.fitOne(pg, out, folded, k, live(k)); ok {
			out, fitted[k.pane] = next, true
			windowed = windowed || w
		}
	}
	if windowed {
		out.fold = folded.lines
	}
	return out
}

// fitOne draws the pane of the expanded row key names into the lines it had on
// the folded page, in cur, a page of as many lines. live says the row is the
// cursor's, whose lines the keys scroll. It says whether the page is windowed.
func (m *Model) fitOne(pg, cur, folded page, key expKey, live bool) (page, bool, bool) {
	e := m.top()
	id := key.id
	// The pane of the row, on the folded page, by the row's identity.
	var fp pane2
	at := -1
	for i, l := range cur.lines {
		if l.ref != nil && l.ref.identity() == id {
			if p := paneAround(cur.lines, i); p.top >= 0 && cur.lines[p.top].text == key.pane {
				fp, at = p, i
				break
			}
		}
	}
	if at < 0 {
		return cur, false, false
	}
	title := key.pane
	// The row as it is laid out expanded, on the page that has it.
	var gx []pline
	for i, l := range pg.lines {
		if l.ref != nil && l.ref.identity() == id {
			xp := paneAround(pg.lines, i)
			if pg.lines[xp.top].text != title {
				continue
			}
			for _, g := range groupsOf(pg.lines[xp.top+1+xp.names : xp.bottom]) {
				if g.row && g.id == id {
					gx = g.lines
				}
			}
			break
		}
	}
	if len(gx) == 0 {
		return cur, false, false
	}
	head := cur.lines[fp.top : fp.top+1+fp.names]
	b0, b1 := fp.top+1+fp.names, fp.bottom
	for _, g := range groupsOf(cur.lines[b0:b1]) {
		if g.row && g.id == id && len(gx) <= len(g.lines) {
			return cur, false, false
		}
	}
	// A folded page taller than the screen is windowed: the lines of the pane that
	// the window does not draw are held by blank lines that carry their rows, and
	// the pane is drawn into the rest.
	lo, hi := b0, b1
	extra, windowed := 0, false
	if h := m.bodyRows(); len(folded.lines) <= h && e.zoom != "" {
		// A zoomed pane has the whole body: it takes the rows the page leaves.
		extra = h - len(folded.lines)
	} else if len(folded.lines) > h {
		windowed = true
		refs := folded.refs()
		sel := min(max(e.sel, 0), len(refs)-1)
		if e.textFocus {
			sel = -1
		}
		start := m.windowStart(folded, sel, h, false)
		if from, to, plain := windowSpan(folded, sel, start, h, e.lead == 0); !plain {
			lo, hi = min(max(from, b0), at), max(min(to, b1), at+1)
		}
	}
	held := func(ls []pline) []pline {
		var out []pline
		for _, l := range ls {
			c := pline{box: boxMid, cont: l.cont, hiddenRefs: append([]rowRef(nil), l.hiddenRefs...)}
			if l.ref != nil {
				c.hiddenRefs = append(c.hiddenRefs, *l.ref)
			}
			out = append(out, c)
		}
		return out
	}
	body := groupsOf(cur.lines[lo:hi])
	cg := -1
	for i, g := range body {
		if g.row && g.id == id {
			cg = i
		}
	}
	if cg < 0 {
		return cur, false, false
	}
	// A row the cursor is not on is read from its first line.
	scrolls := e
	if !live {
		scrolls = &navEntry{}
	}
	var lines []pline
	lines = append(lines, held(cur.lines[b0:lo])...)
	lines = append(lines, m.fitRows(body, cg, gx, hi-lo+extra, scrolls)...)
	lines = append(lines, held(cur.lines[hi:b1])...)
	out := cur
	out.lines = append(append(append(append([]pline(nil), cur.lines[:fp.top]...), head...), lines...), cur.lines[fp.bottom:]...)
	return out, windowed, true
}

// pane2 is the place of a pane on a page: the lines of its top edge, its names
// and its bottom edge.
type pane2 struct{ top, names, bottom int }

// paneAround is the pane the line i is in.
func paneAround(lines []pline, i int) pane2 {
	p := pane2{top: -1, bottom: len(lines) - 1}
	for k := i; k >= 0; k-- {
		if lines[k].box == boxTop {
			p.top = k
			break
		}
	}
	for k := i; k < len(lines); k++ {
		if lines[k].box == boxBottom {
			p.bottom = k
			break
		}
	}
	if p.top < 0 {
		return p
	}
	for k := p.top + 1; k < p.bottom && lines[k].names; k++ {
		p.names++
	}
	return p
}

// fitRows draws the pane's rows, body, in room lines with the cursor's row cg
// laid out as gx: the rows around it that fit beside it, a count of the rest, and
// the row itself cut at the line it is scrolled to when it is taller than what is
// left. The lines are as many as room, and the rows left out ride the counts.
func (m *Model) fitRows(body []group, cg int, gx []pline, room int, e *navEntry) []pline {
	count := func(lo, hi int, arrow string) pline {
		return pline{text: m.st.dim(arrow + " " + strconv.Itoa(len(refsOfGroups(body[lo:hi]))) + " more"), box: boxMid, hiddenRefs: refsOfGroups(body[lo:hi])}
	}
	marks := func(lo, hi int) int {
		n := 0
		if lo > 0 {
			n++
		}
		if hi < len(body) {
			n++
		}
		return n
	}
	size := len(gx)
	lo, hi := cg, cg+1
	if size+marks(lo, hi) > room {
		// A row taller than its room: the neighbours are counted only when there
		// is room for them and a line of the row beside.
		return m.cutRow(body, cg, gx, room, e)
	}
	for grew := true; grew; {
		grew = false
		if hi < len(body) && size+len(body[hi].lines)+marks(lo, hi+1) <= room {
			size += len(body[hi].lines)
			hi++
			grew = true
		}
		if lo > 0 && size+len(body[lo-1].lines)+marks(lo-1, hi) <= room {
			size += len(body[lo-1].lines)
			lo--
			grew = true
		}
	}
	var out []pline
	if lo > 0 {
		out = append(out, count(0, lo, "↑"))
	}
	for i := lo; i < hi; i++ {
		if i == cg {
			out = append(out, gx...)
		} else {
			out = append(out, body[i].lines...)
		}
	}
	if hi < len(body) {
		out = append(out, count(hi, len(body), "↓"))
	}
	for len(out) < room {
		out = append(out, pline{box: boxMid})
	}
	return out
}

// cutRow draws the row gx alone in room lines, scrolled to the line the person
// read to, with a count of the lines above and below it and of the rows around
// it, each kept only while a line of the row is left beside it.
func (m *Model) cutRow(body []group, cg int, gx []pline, room int, e *navEntry) []pline {
	total := len(gx)
	id := body[cg].id
	// What stands around the row: a count of the rows above and below when there
	// is room for them and for two lines of the row.
	above, below := cg > 0, cg < len(body)-1
	room2 := room
	var upRows, downRows pline
	if room >= 4 {
		if above {
			upRows = pline{text: m.st.dim("↑ " + strconv.Itoa(len(refsOfGroups(body[:cg]))) + " more"), box: boxMid, hiddenRefs: refsOfGroups(body[:cg])}
			room2--
		}
		if below {
			downRows = pline{text: m.st.dim("↓ " + strconv.Itoa(len(refsOfGroups(body[cg+1:]))) + " more"), box: boxMid, hiddenRefs: refsOfGroups(body[cg+1:])}
			room2--
		}
	}
	// The part of the row drawn: marks above and below it only while a line of
	// the row is left beside them.
	maxScroll := 0
	if room2 >= 2 {
		maxScroll = total - (room2 - 1)
	} else {
		maxScroll = total - 1
	}
	maxScroll = max(0, maxScroll)
	scroll := 0
	if e.rowScrollOf == id {
		scroll = e.rowScroll
	}
	scroll = min(max(scroll, 0), maxScroll)
	e.rowScroll, e.rowScrollOf = scroll, id

	top := scroll > 0 && room2 >= 2
	content := room2
	if top {
		content--
	}
	bottom := scroll+content < total && content >= 2
	if bottom {
		content--
	} else {
		content = min(content, total-scroll)
	}
	lineMark := func(arrow string, n int) pline {
		return pline{text: m.st.dim(fmt.Sprintf("%s %d more %s", arrow, n, plural(n, "line", "lines"))), box: boxMid, cont: true}
	}
	var row []pline
	if top {
		mk := lineMark("↑", scroll)
		mk.ref, mk.cont = gx[0].ref, false
		row = append(row, mk)
	}
	for i := 0; i < content; i++ {
		ln := gx[scroll+i]
		if top || i > 0 {
			ln.ref, ln.cont = nil, true
		} else {
			// The first line drawn is the row's place, whatever line of it that is.
			ln.ref, ln.cont = gx[0].ref, false
		}
		row = append(row, ln)
	}
	if bottom {
		row = append(row, lineMark("↓", total-scroll-content))
	}
	var out []pline
	if upRows.text != "" {
		out = append(out, upRows)
	}
	out = append(out, row...)
	if downRows.text != "" {
		out = append(out, downRows)
	}
	// The rows left out of a pane with no room for their counts are still the
	// page's rows: the lines at its ends carry them, so the cursor's place and the
	// keys count them as they did folded.
	if upRows.text == "" && cg > 0 {
		out[0].hiddenRefs = append(refsOfGroups(body[:cg]), out[0].hiddenRefs...)
	}
	if downRows.text == "" && cg < len(body)-1 {
		last := &out[len(out)-1]
		last.hiddenRefs = append(last.hiddenRefs, refsOfGroups(body[cg+1:])...)
	}
	for len(out) < room {
		out = append(out, pline{box: boxMid})
	}
	if e == m.top() {
		m.tall = &tallInfo{id: id, total: total, part: len(row), max: maxScroll}
	}
	return out
}

// scrollTall moves the part of a row read by lines delta lines, and says whether
// it did: a press that finds the part at its first or last line leaves the move
// to the cursor, which goes to the neighbouring row.
func (m *Model) scrollTall(pg page, delta int) bool {
	t := m.tall
	e := m.top()
	if t == nil || e.rowScrollOf != t.id || delta == 0 || delta <= -(1<<19) || delta >= 1<<19 {
		return false
	}
	to := min(max(e.rowScroll+delta, 0), t.maxScroll())
	if to == e.rowScroll {
		return false
	}
	e.rowScroll = to
	return true
}
