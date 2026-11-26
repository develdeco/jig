package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// wheelRows is how many rows a tick of the mouse wheel moves.
const wheelRows = 3

// hitMap is where the last frame drew the body: the terminal row it began at,
// how many lines of the page it drew, which line of the page its first row was
// (after the edge and the mark a window puts before its lines), and for each
// line of the page the place of the row the cursor can stand on, -1 for a line
// that is no row. A click is found through it, so it reads the window the
// frame drew and no other.
type hitMap struct {
	top, rows, start int
	// sel is, for each line of the page, its place among the rows, else -1.
	sel []int
	// owner is, for each line of the page, the place among the rows of the row
	// the line belongs to (itself, or the row a continuation row hangs from), else
	// -1; cuts are the cut marks the line carries.
	owner []int
	cuts  [][]cutMark
	// panes are the panes of the page, for a click on one to focus it.
	panes []pane
}

// recordHit notes what the frame drew of the body, for a click to be read against.
func (m *Model) recordHit(l screenLayout, from, to, prefix int) {
	if m.help != nil {
		m.hit = nil
		return
	}
	h := &hitMap{top: len(l.head) + prefix, rows: to - from, start: from, panes: l.pg.panes()}
	n, cur := 0, -1
	for _, line := range l.pg.lines {
		h.cuts = append(h.cuts, line.cuts)
		if line.ref == nil {
			h.sel = append(h.sel, -1)
			if !line.cont || line.names {
				// Anything but a row's continuation row ends the row above it.
				cur = -1
			}
			h.owner = append(h.owner, cur)
			continue
		}
		h.sel = append(h.sel, n)
		h.owner = append(h.owner, n)
		cur = n
		n++
	}
	m.hit = h
}

// mouse takes a mouse event in: the wheel moves the cursor three rows, scrolls
// a page that has none, and scrolls the keys list while it is up; a click on a
// row of the body puts the cursor on it and opens nothing.
func (m *Model) mouse(k tea.MouseMsg) tea.Cmd {
	if k.Action != tea.MouseActionPress || m.input != nil || m.ask != nil {
		return nil
	}
	m.followNew = false
	switch k.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta := wheelRows
		if k.Button == tea.MouseButtonWheelUp {
			delta = -wheelRows
		}
		if m.help != nil {
			m.help.scroll += delta
			m.clampHelp()
			return nil
		}
		m.move(delta)
	case tea.MouseButtonLeft:
		m.click(k.X, k.Y)
	}
	return nil
}

// onCut says whether a click at terminal column x lands on a cut mark of the
// line: the mark's cell, or the cell before it, so a pointer a cell short of a
// one-cell "…" still reaches it.
func onCut(cuts []cutMark, x int) bool {
	for _, c := range cuts {
		if at := x - frameLead; at == c.x || at == c.x-1 {
			return true
		}
	}
	return false
}

// click puts the cursor on the row drawn at terminal row y, when one is, and
// expands the row's holder when x is on a cut mark of its lines.
func (m *Model) click(x, y int) {
	h := m.hit
	if m.help != nil || h == nil || y < h.top || y >= h.top+h.rows {
		return
	}
	line := h.start + y - h.top
	if line < 0 || line >= len(h.sel) {
		return
	}
	if h.sel[line] < 0 && h.owner[line] >= 0 {
		// A row's continuation row is the row's own: a click on it selects the
		// row, and one on a mark of it expands the row.
		m.selectRow(h.owner[line])
		if onCut(h.cuts[line], x) {
			m.expandHolder(false)
		}
		return
	}
	if h.sel[line] < 0 {
		// A click on a pane's text or edge gives the pane the focus.
		if m.top().screen != screenMain {
			for _, p := range h.panes {
				if line >= p.top && line <= p.bottom {
					m.focusPane(p.title)
					if p.rows == 0 && onCut(h.cuts[line], x) {
						// A mark of a pane with no cursor rows is the pane's own.
						m.expandHolder(false)
					}
					break
				}
			}
		}
		return
	}
	m.selectRow(h.sel[line])
	if onCut(h.cuts[line], x) {
		m.expandHolder(false)
	}
}

// selectRow puts the cursor on the sel-th row of the page.
func (m *Model) selectRow(sel int) {
	e := m.top()
	e.sel, e.follow, e.lead = sel, false, 0
	// The pane clicked has the focus, and its cursor is the row clicked, at once.
	m.syncPanes(m.geometry())
	m.noteCursor()
}
