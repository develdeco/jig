package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A panel is a run of a page's lines drawn inside a rounded frame, with a
// title on its top edge and a few words at its right: the steps of a ticket,
// a step's activity. The frame is drawn when the page is windowed, at the
// screen's width, so a panel's lines are written without knowing it.

// How a page line sits in a panel.
const (
	boxNone   = iota
	boxTop    // the top edge, with the title and the right-hand words
	boxMid    // a line inside the frame
	boxBottom // the bottom edge
)

// boxTop starts a panel titled title, with right at the right of its top
// edge.
func (p *page) boxTop(title, right string) {
	p.lines = append(p.lines, pline{text: title, right: right, box: boxTop})
}

// boxAdd adds a line inside the panel.
func (p *page) boxAdd(text string) {
	p.lines = append(p.lines, pline{text: text, box: boxMid})
}

// boxUnder adds a line inside the panel that a list draws under its row: the
// row's detail. A window that moves with a cursor never begins on one.
func (p *page) boxUnder(text string) {
	p.lines = append(p.lines, pline{text: text, box: boxMid, under: true})
}

// boxRow adds the lines the allocator laid a row out in, inside the panel: the
// first is the row, standing for ref when the cursor can stand on it, and the
// rest are its continuation rows, with the cuts the row carries.
func (p *page) boxRow(ref *rowRef, r laidRow) {
	for i, l := range r.lines {
		pl := pline{text: l, box: boxMid, cont: i > 0}
		if i == 0 {
			pl.ref = ref
		}
		if i < len(r.cuts) {
			pl.cuts = r.cuts[i]
		}
		p.lines = append(p.lines, pl)
	}
}

// frameLead is the cells a panel's line has before its text: the margin, the
// edge and the cursor's mark.
const frameLead = 5

// innerWidth is the cells a panel's text has: the width less the frame, the
// cursor's mark and the space before the right edge.
func (m *Model) innerWidth() int { return max(1, m.width-7) }

// boxRows lays rows out under cols in the panel's width and adds them to it.
// None is a row the cursor can stand on.
func (m *Model) boxRows(p *page, cols []column, rows [][]string) {
	for _, row := range layoutMarked(cols, rows, m.innerWidth(), wrapRows, m.wholePane(p.paneTitle(), len(rows))) {
		p.boxRow(nil, row)
	}
}

// wholePane is the rows of a pane with no cursor rows laid out whole when the
// person has expanded the pane: every row of it, else none.
func (m *Model) wholePane(pane string, n int) []bool {
	if !m.paneExpanded(pane) {
		return nil
	}
	whole := make([]bool, n)
	for i := range whole {
		whole[i] = true
	}
	return whole
}

// boxHeight is how many lines boxRows would add to the pane titled pane.
func (m *Model) boxHeight(pane string, cols []column, rows [][]string) int {
	n := 0
	for _, row := range layoutMarked(cols, rows, m.innerWidth(), wrapRows, m.wholePane(pane, len(rows))) {
		n += len(row.lines)
	}
	return n
}

// runColumns are the columns of the Run and Detail panels: a label and the
// words after it.
var runColumns = []column{{}, {free: true, gap: 1}}

// runCells are rows of the Run and Detail panels as cells.
func (m *Model) runCells(rows []runRow) [][]string {
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = []string{m.st.dim(r.label), r.text}
	}
	return cells
}

// runBoxRows adds the rows of a Run or Detail panel.
// A row that is an ask carries the ask on every line it wraps to, so the
// window can tell whether the ask was drawn whole.
func (m *Model) runBoxRows(p *page, rows []runRow) {
	for i, row := range layoutMarked(runColumns, m.runCells(rows), m.innerWidth(), wrapRows, m.wholePane(p.paneTitle(), len(rows))) {
		at := len(p.lines)
		p.boxRow(nil, row)
		for k := at; k < len(p.lines); k++ {
			p.lines[k].ask = rows[i].ask
		}
	}
}

// runHeight is the lines runBoxRows adds for rows in the pane titled pane.
func (m *Model) runHeight(pane string, rows []runRow) int {
	return m.boxHeight(pane, runColumns, m.runCells(rows))
}

// boxEnd closes the panel.
func (p *page) boxEnd() {
	p.lines = append(p.lines, pline{box: boxBottom})
}

// edgeKey is a piece of a panel's border and whether the panel is focused.
type edgeKey struct {
	focus bool
	text  string
}

// edgeCache holds the pieces of border a renderer has drawn, so a frame draws
// each once however many rows share it.
type edgeCache struct {
	r    *lipgloss.Renderer
	text map[edgeKey]string
}

// edge is a piece of a panel's border drawn in its color, remembered for the
// renderer the model draws with.
func (m *Model) edge(focus bool, text string) string {
	if m.edges.text == nil || m.edges.r != m.st.r || len(m.edges.text) > 512 {
		m.edges = edgeCache{r: m.st.r, text: map[edgeKey]string{}}
	}
	k := edgeKey{focus, text}
	if s, ok := m.edges.text[k]; ok {
		return s
	}
	s := m.st.dim(text)
	if focus {
		s = m.st.key(text)
	}
	m.edges.text[k] = s
	return s
}

// frameLine draws a page line of a panel w cells wide, two cells in from the
// left like every other line; marker is the cursor's mark for a row ("▸ " or
// two spaces). A line is never wider than w, whatever its text.
func (m *Model) frameLine(l pline, marker string, w int, lit bool) string {
	inner := w - 7 // "  │" + marker + text + " │"
	// A focused panel's border is drawn in the key color.
	edge := func(text string) string { return m.edge(l.focus, text) }
	switch l.box {
	case boxTop:
		title := " " + m.st.bold(l.text) + " "
		right := ""
		if l.right != "" {
			right = " " + m.st.dim(l.right) + " "
		}
		fill := w - 6 - width(title) - width(right) // "  ╭─" + title + fill + right + "─╮"
		if fill < 1 {
			right = ""
			fill = max(1, w-6-width(title))
		}
		return fit("  "+edge("╭─")+title+edge(strings.Repeat("─", fill))+right+edge("─╮"), w)
	case boxBottom:
		return "  " + edge("╰"+strings.Repeat("─", max(0, w-4))+"╯")
	}
	body := marker + pad(l.text, max(0, inner)) + " "
	if lit {
		// The row is lit from border to border.
		body = m.st.selectedRow(body, max(0, inner)+3)
	}
	line := "  " + edge("│") + body + edge("│")
	if w < 8 {
		// A screen with no room for a row's frame cuts it.
		return fit(line, w)
	}
	return line
}
