package tui

import "fmt"

// A row taller than its room is read by lines. An expanded row under the cursor
// that the window cannot show whole is drawn as the part of it that fits: the
// lines from where the person has scrolled to, with a dim count of the lines
// above and of the lines below it when there are any. `up` and `down` move that
// part a line; one past the row's first or last line moves the cursor to the
// neighbouring row. A pane that is zoomed has the body for its room, so the same
// row may fit there whole.

// tallInfo is what the last frame drew of a row it had to read by lines: its
// identity, how many lines it has and how many of them the window held.
type tallInfo struct {
	id    rowIdentity
	total int
	part  int
}

// maxScroll is the most lines of the row that can be above the part shown: with
// the line of the count above it, the part ends on the row's last line.
func (t tallInfo) maxScroll() int { return max(0, t.total-t.part+1) }

// tallRow draws the expanded row under the cursor by lines when the window
// cannot hold it whole: it replaces the row's lines in the page of the layout
// with the part that fits, and says what it did in m.tall.
func (m *Model) tallRow(l *screenLayout) {
	m.tall = nil
	e := m.top()
	if m.help != nil || e.textFocus || len(e.expanded) == 0 || l.pageH < 3 {
		return
	}
	lines := l.pg.lines
	first, k := -1, 0
	for i, ln := range lines {
		if ln.ref == nil {
			continue
		}
		if k == l.sel {
			first = i
			break
		}
		k++
	}
	if first < 0 || !e.isExpanded(e.paneFocus, lines[first].ref) {
		return
	}
	last := first + 1
	for last < len(lines) && (lines[last].cont || lines[last].under) && lines[last].ref == nil && !lines[last].names {
		last++
	}
	total := last - first
	if total < 3 {
		return
	}
	h := l.pageH
	shown := func(pg page, rowEnd int) bool {
		from, to, _ := windowSpan(pg, l.sel, m.windowStart(pg, l.sel, h, false), h, true)
		return from <= first && to >= rowEnd
	}
	// A row never takes more than the room its pane was given, nor more than the
	// body holds.
	capLines := h
	for i := first; i >= 0; i-- {
		if lines[i].box == boxTop {
			if r, ok := l.pg.rooms[lines[i].text]; ok && r > 0 {
				capLines = min(h, r)
			}
			break
		}
	}
	if total <= capLines && shown(l.pg, last) {
		return
	}
	id := lines[first].ref.identity()
	part := 0
	for p := min(total-1, capLines); p >= 3; p-- {
		if shown(m.tallPage(l.pg, first, last, 0, p), first+p) {
			part = p
			break
		}
	}
	if part == 0 {
		return
	}
	scroll := 0
	if e.rowScrollOf == id {
		scroll = e.rowScroll
	}
	t := tallInfo{id: id, total: total, part: part}
	scroll = min(max(scroll, 0), t.maxScroll())
	e.rowScroll, e.rowScrollOf = scroll, id
	l.pg = m.tallPage(l.pg, first, last, scroll, part)
	m.tall = &t
}

// tallPage is pg with the row of lines first to last-1 replaced by the part of
// it that holds part lines when its first scroll lines are left above: the lines
// themselves, and a dim count in the place of the first when lines are above and
// in the place of the last when lines are below.
func (m *Model) tallPage(pg page, first, last, scroll, part int) page {
	lines := pg.lines
	total := last - first
	head := lines[first]
	top := scroll > 0
	// The lines the part holds when nothing is below it, else one fewer for the
	// count of what is.
	content := part
	if top {
		content--
	}
	bottom := scroll+content < total
	if bottom {
		content--
	} else {
		content = total - scroll
	}
	row := make([]pline, 0, part)
	mark := func(text string, ref *rowRef) pline {
		return pline{text: m.st.dim(text), box: head.box, focus: head.focus, ref: ref, cont: ref == nil}
	}
	if top {
		row = append(row, mark(fmt.Sprintf("↑ %d more %s", scroll, plural(scroll, "line", "lines")), head.ref))
	}
	for i := 0; i < content; i++ {
		ln := lines[first+scroll+i]
		if top || i > 0 {
			ln.ref, ln.cont = nil, true
		}
		row = append(row, ln)
	}
	if bottom {
		row = append(row, mark(fmt.Sprintf("↓ %d more %s", total-scroll-content, plural(total-scroll-content, "line", "lines")), nil))
	}
	out := pg
	out.lines = append(append(append([]pline(nil), lines[:first]...), row...), lines[last:]...)
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
	refs := pg.refs()
	if e.sel < 0 || e.sel >= len(refs) || refs[e.sel].identity() != t.id {
		return false
	}
	to := min(max(e.rowScroll+delta, 0), t.maxScroll())
	if to == e.rowScroll {
		return false
	}
	e.rowScroll = to
	return true
}
