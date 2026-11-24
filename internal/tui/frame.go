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

// boxEnd closes the panel.
func (p *page) boxEnd() {
	p.lines = append(p.lines, pline{box: boxBottom})
}

// frameLine draws a page line of a panel w cells wide, two cells in from the
// left like every other line; marker is the cursor's mark for a row ("▸ " or
// two spaces).
func (m *Model) frameLine(l pline, marker string, w int, lit bool) string {
	inner := w - 7 // "  │" + marker + text + " │"
	edge := m.st.dim
	if l.focus {
		// A focused panel's border is drawn in the key color.
		edge = m.st.key
	}
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
		return "  " + edge("╭─") + title + edge(strings.Repeat("─", fill)) + right + edge("─╮")
	case boxBottom:
		return "  " + edge("╰"+strings.Repeat("─", max(0, w-4))+"╯")
	}
	body := marker + pad(l.text, max(0, inner)) + " "
	if lit {
		// The row is lit from border to border.
		body = m.st.lit(body, max(0, inner)+3)
	}
	return "  " + edge("│") + body + edge("│")
}
