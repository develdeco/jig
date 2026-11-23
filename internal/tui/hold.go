package tui

import (
	"fmt"
	"strings"
)

// A line that comes in leaves the reader where they were. Newest first puts a
// new line above what is being read, so the screen holds two things across a
// frame whose page differs from the one before: the cursor stays on its row,
// found again by its ref, and a page read below its top keeps showing the lines
// it showed, with a row at the window's top counting the rows that came in
// above. Both are settled by the frame itself, from what the last frame drew,
// so a message costs no page of its own.

// held is what the last frame drew of the screen on top: its lines, its rows,
// where the cursor and the window stood.
type held struct {
	idx    int // the entry's place in the way in
	screen screenKind
	ticket string
	slice  string
	chart  string
	lines  []string
	refs   []rowRef
	sel    int
	start  int // the window's first line before the count row moved it
	rows   int // the window's height
	scroll int
	lead   int
}

// same says whether the entry on top is the one the frame drew.
func (h *held) same(m *Model) bool {
	if len(m.nav)-1 != h.idx {
		return false
	}
	e := m.top()
	return e.screen == h.screen && e.ticket == h.ticket && e.slice == h.slice && e.chart == h.chart
}

// settle puts the reader back where the last frame left them when the page
// changed under them: the cursor on its row, and a page that was read below its
// top on the lines it showed, the rows that came in above counted for the row
// that says so. A cursor or a window the reader moved since is left where that
// put it.
func (m *Model) settle(l *screenLayout) {
	h := m.seen
	if h == nil || !h.same(m) {
		return
	}
	e := m.top()
	refs := l.pg.refs()
	if !e.follow && e.sel == h.sel && h.sel < len(h.refs) && !(h.sel < len(refs) && refs[h.sel] == h.refs[h.sel]) {
		for i, r := range refs {
			if r == h.refs[h.sel] {
				e.sel = i
				l.sel = min(i, len(refs)-1)
				break
			}
		}
	}
	if h.start == 0 || h.rows != l.pageH || e.scroll != h.scroll || e.lead != h.lead {
		return
	}
	shift, rows := lineShift(h, l.pg)
	if shift <= 0 {
		return
	}
	if len(refs) == 0 {
		e.scroll = h.start + shift
	} else {
		now := m.windowStart(l.pg, l.sel, l.pageH, false)
		e.lead = max(0, e.lead+now-(h.start+shift))
	}
	e.fresh += rows
}

// rest notes what the frame drew, for the next one to settle against; start is
// the window's first line before the count row moved it.
func (m *Model) rest(l screenLayout, start int) {
	e := m.top()
	h := &held{idx: len(m.nav) - 1, screen: e.screen, ticket: e.ticket, slice: e.slice, chart: e.chart,
		refs: l.pg.refs(), sel: e.sel, start: start, rows: l.pageH, scroll: e.scroll, lead: e.lead}
	h.lines = make([]string, len(l.pg.lines))
	for i, line := range l.pg.lines {
		h.lines[i] = line.text
	}
	m.seen = h
}

// countRow is where the window starts and whether its first row is the count of
// the rows that came in above it, for a page read below its top that rows came
// in above. The count says so in the row above the line the window began at: the
// window starts one line up, and that row is the count. That drops the window's
// last line, so when that is the page's end or the line the cursor stands on,
// the count takes the first line's row instead and the window stays where it was.
func (m *Model) countRow(pg page, sel, start, h int) (int, bool) {
	e := m.top()
	if start == 0 {
		e.fresh = 0
	}
	if e.fresh == 0 || start == 0 {
		return start, false
	}
	selLine, n := -1, 0
	for i, l := range pg.lines {
		if l.ref == nil {
			continue
		}
		if n == sel {
			selLine = i
		}
		n++
	}
	if last := start + h - 1; last != selLine && (last < len(pg.lines)-1 || start == selLine) {
		start--
	}
	return start, true
}

// lineShift is how many lines the first line the reader had in the window moved
// down by, which is the lines that came in above it, and how many of those are
// rows of their own: a line that goes on the one above it (a record's second
// line) is not another row. Both are 0 when the page no longer has that line,
// followed by the one under it, as it was.
func lineShift(h *held, pg page) (shift, rows int) {
	a := -1
	for i := h.start; i < min(len(h.lines), h.start+h.rows); i++ {
		if h.lines[i] != "" {
			a = i
			break
		}
	}
	if a < 0 {
		return 0, 0
	}
	best := -1
	for j, l := range pg.lines {
		if l.text != h.lines[a] {
			continue
		}
		if a+1 < len(h.lines) && (j+1 >= len(pg.lines) || pg.lines[j+1].text != h.lines[a+1]) {
			continue
		}
		if best < 0 || abs(j-a) < abs(best-a) {
			best = j
		}
	}
	if best <= a {
		return 0, 0
	}
	for _, l := range pg.lines[a:best] {
		if strings.TrimSpace(l.text) != "" && !strings.HasPrefix(l.text, "    ") {
			rows++
		}
	}
	return best - a, max(rows, 1)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// newRow is the row at the top of a page read below its top that counts the
// rows that came in above the window.
func (m *Model) newRow(n int) string {
	return m.st.dim(fmt.Sprintf("↑ %d new", n))
}
