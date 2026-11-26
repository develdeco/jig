package tui

import "slices"

// `z` zooms the focused pane: the page the screen draws is that pane alone, with
// its title, its cursor and its scroll as they are, over the whole body. The zoom
// is a state of the screen, kept with it while it is on the stack. A pane that
// draws only a window of its rows (panes.go) is built with the room of the whole
// body (zoomRoom), so the window it draws is as wide as the screen allows.

// zoomRoom is how many lines of rows the pane titled title may take when it is
// the one zoomed: the whole body, less the pane's own edges and names. It says
// whether the pane is zoomed.
func (m *Model) zoomRoom(title string) (int, bool) {
	if m.top().zoom != title {
		return 0, false
	}
	return max(3, m.bodyRows()-3), true
}

// zoomed says the pane titled title is the one drawn over the whole body: a pane
// of text is then built whole, not as the allocator cuts it to share the body.
func (m *Model) zoomed(title string) bool { return m.top().zoom == title }

// zoomPage is pg cut down to the zoomed pane. A pane the screen no longer has
// ends the zoom.
func (m *Model) zoomPage(pg page) page {
	e := m.top()
	ps := pg.panes()
	i := slices.IndexFunc(ps, func(p pane) bool { return p.title == e.zoom })
	if i < 0 {
		e.zoom = ""
		return pg
	}
	p := ps[i]
	out := page{lines: slices.Clone(pg.lines[p.top : p.bottom+1])}
	for _, q := range ps {
		out.allPanes = append(out.allPanes, q.title)
	}
	if full, ok := pg.windowed[p.title]; ok {
		out.windowed = map[string][]rowRef{p.title: full}
	}
	return out
}

// toggleZoom is `z`: the focused pane is drawn over the whole body, or the
// screen is drawn again.
func (m *Model) toggleZoom() {
	e := m.top()
	m.syncPanes(m.geometry())
	if e.zoom != "" {
		m.setZoom("")
		return
	}
	if e.paneFocus != "" {
		m.setZoom(e.paneFocus)
	}
}

// setZoom draws the pane titled title over the whole body, "" for the screen. The
// cursor is found again by its row's identity on the next page.
func (m *Model) setZoom(title string) {
	e := m.top()
	was := e.zoom
	e.zoom = title
	// What the pane draws has changed: what the last frame held is not what a new
	// line is read against.
	m.seen = nil
	e.seenSel, e.seenSet = e.sel, true
	if e.textFocus {
		// A pane of text is read from its top edge in either view.
		e.scroll = 0
		if title == "" && was != "" {
			e.scroll = m.paneTop(was)
		}
	}
}

// paneTop is the line to scroll the page to so the pane titled title is at its
// top, when the screen is drawn whole.
func (m *Model) paneTop(title string) int {
	pg := m.unzoomed()
	for _, p := range pg.panes() {
		if p.title == title {
			return clampStart(pg.lines, m.bodyRows(), p.top)
		}
	}
	return 0
}

// unzoomed is the screen's page with no pane zoomed: the one the focus moves over.
func (m *Model) unzoomed() page {
	e := m.top()
	z := e.zoom
	e.zoom = ""
	pg := m.page()
	e.zoom = z
	return pg
}
