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
	n := 0
	for _, line := range l.pg.lines {
		if line.ref == nil {
			h.sel = append(h.sel, -1)
			continue
		}
		h.sel = append(h.sel, n)
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
		m.click(k.Y)
	}
	return nil
}

// click puts the cursor on the row drawn at terminal row y, when one is.
func (m *Model) click(y int) {
	h := m.hit
	if m.help != nil || h == nil || y < h.top || y >= h.top+h.rows {
		return
	}
	line := h.start + y - h.top
	if line < 0 || line >= len(h.sel) {
		return
	}
	if h.sel[line] < 0 {
		// A click on a pane's text or edge gives the pane the focus.
		if m.top().screen != screenMain {
			for _, p := range h.panes {
				if line >= p.top && line <= p.bottom {
					m.focusPane(p.title)
					break
				}
			}
		}
		return
	}
	e := m.top()
	e.sel, e.follow, e.lead = h.sel[line], false, 0
	m.noteCursor()
}
