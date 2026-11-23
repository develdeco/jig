package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// A line that comes in leaves the reader where they were. Newest first puts a
// new line above what is being read, so the screen holds two things across a
// message that changes a page: the cursor stays on its row, found again by its
// ref, and a page read below its top keeps showing the lines it showed, with a
// row at the window's top counting the lines that came in above.

// held is what the screen on top stood on before a message: the page it drew,
// where the cursor and the window were.
type held struct {
	idx    int // the entry's place in the way in
	screen screenKind
	ticket string
	slice  string
	chart  string
	lines  []string
	refs   []rowRef
	sel    int
	start  int
	rows   int
}

// holdBefore notes where the reader stands before msg, when msg brings what a
// page is drawn from: lines, a status, a roster, a chart or a brief. A key is
// the reader's own move, and a tick or a resize leaves the lines as they are.
func (m *Model) holdBefore(msg tea.Msg) *held {
	switch msg.(type) {
	case eventsMsg, statusMsg, rosterMsg, chartMsg, briefMsg:
	default:
		return nil
	}
	if len(m.nav) == 0 || m.width <= 0 || m.height <= 0 {
		return nil
	}
	e := m.top()
	pg := m.page()
	h := &held{idx: len(m.nav) - 1, screen: e.screen, ticket: e.ticket, slice: e.slice, chart: e.chart, refs: pg.refs(), sel: e.sel, rows: m.bodyRows()}
	for _, l := range pg.lines {
		h.lines = append(h.lines, l.text)
	}
	h.start = m.windowStart(pg, e.sel, h.rows, false)
	return h
}

// holdAfter puts the reader back where holdBefore found them: the cursor on its
// row, and a page that was read below its top on the lines it showed, the lines
// that came in above counted for the row that says so.
func (m *Model) holdAfter(h *held) {
	if h == nil || len(m.nav)-1 != h.idx {
		return
	}
	e := m.top()
	if e.screen != h.screen || e.ticket != h.ticket || e.slice != h.slice || e.chart != h.chart {
		return
	}
	pg := m.page()
	refs := pg.refs()
	// The cursor is a row: when the lines under it moved, it moves with its row.
	// A cursor something else moved in the same message is left where that put it.
	if !e.follow && e.sel == h.sel && h.sel < len(h.refs) && !(h.sel < len(refs) && refs[h.sel] == h.refs[h.sel]) {
		for i, r := range refs {
			if r == h.refs[h.sel] {
				e.sel = i
				break
			}
		}
	}
	if h.start == 0 {
		return
	}
	shift := lineShift(h, pg)
	if shift <= 0 {
		return
	}
	if len(refs) == 0 {
		e.scroll = h.start + shift
	} else {
		now := m.windowStart(pg, e.sel, h.rows, false)
		e.lead = max(0, e.lead+now-(h.start+shift))
	}
	e.fresh += shift
}

// lineShift is how many lines the first line the reader had in the window
// moved down by: the lines that came in above it. It is 0 when the page no
// longer has that line, followed by the one under it, as it was.
func lineShift(h *held, pg page) int {
	a := -1
	for i := h.start; i < min(len(h.lines), h.start+h.rows); i++ {
		if h.lines[i] != "" {
			a = i
			break
		}
	}
	if a < 0 {
		return 0
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
	if best < 0 {
		return 0
	}
	return best - a
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// newRow is the row at the top of a page read below its top that counts the
// lines that came in above the window.
func (m *Model) newRow(n int) string {
	return m.st.dim(fmt.Sprintf("↑ %d new", n))
}
