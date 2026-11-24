package tui

import "strings"

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

// boxRow adds a row the cursor can stand on inside the panel.
func (p *page) boxRow(ref rowRef, text string) {
	p.lines = append(p.lines, pline{text: text, ref: &ref, box: boxMid})
}

// boxLaid adds the lines the allocator laid a row out in, inside the panel:
// the first is the row, standing for ref when the cursor can stand on it, and
// the rest are its continuation rows.
func (p *page) boxLaid(ref *rowRef, lines []string) {
	for i, l := range lines {
		pl := pline{text: l, box: boxMid, cont: i > 0}
		if i == 0 {
			pl.ref = ref
		}
		p.lines = append(p.lines, pl)
	}
}

// innerWidth is the cells a panel's text has: the width less the frame, the
// cursor's mark and the space before the right edge.
func (m *Model) innerWidth() int { return max(1, m.width-7) }

// boxRows lays rows out under cols in the panel's width and adds them to it.
// None is a row the cursor can stand on.
func (m *Model) boxRows(p *page, cols []column, rows [][]string) {
	for _, lines := range layout(cols, rows, m.innerWidth()) {
		p.boxLaid(nil, lines)
	}
}

// boxHeight is how many lines boxRows would add.
func (m *Model) boxHeight(cols []column, rows [][]string) int {
	n := 0
	for _, lines := range layout(cols, rows, m.innerWidth()) {
		n += len(lines)
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
	for i, lines := range layout(runColumns, m.runCells(rows), m.innerWidth()) {
		at := len(p.lines)
		p.boxLaid(nil, lines)
		for k := at; k < len(p.lines); k++ {
			p.lines[k].ask = rows[i].ask
		}
	}
}

// runHeight is the lines runBoxRows adds for rows.
func (m *Model) runHeight(rows []runRow) int { return m.boxHeight(runColumns, m.runCells(rows)) }

// boxEnd closes the panel.
func (p *page) boxEnd() {
	p.lines = append(p.lines, pline{box: boxBottom})
}

// frameLine draws a page line of a panel w cells wide, two cells in from the
// left like every other line; marker is the cursor's mark for a row ("▸ " or
// two spaces).
func (m *Model) frameLine(l pline, marker string, w int) string {
	inner := w - 7 // "  │" + marker + text + " │"
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
		return "  " + m.st.dim("╭─") + title + m.st.dim(strings.Repeat("─", fill)) + right + m.st.dim("─╮")
	case boxBottom:
		return "  " + m.st.dim("╰"+strings.Repeat("─", max(0, w-4))+"╯")
	}
	return "  " + m.st.dim("│") + marker + pad(l.text, max(0, inner)) + " " + m.st.dim("│")
}
