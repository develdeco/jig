package tui

import "fmt"

// chartCells are the chart's row on the project's list as cells: its state, its
// name, its title, "chart", and how many of its tickets are published and
// whether one needs the person.
func (m *Model) chartCells(name string) []string {
	c := m.w.charts[name]
	rows := m.statusRows(c)
	total, done, needs := chartProgress(rows)
	state, _ := m.chartStatus(c, rows)
	title := c.title()
	if title == name {
		title = ""
	}
	counts := m.st.dim("not graduated")
	if total > 0 {
		counts = m.st.dim(fmt.Sprintf("%d of %d published", done, total))
	}
	if needs > 0 {
		counts += m.st.state(StateNeedsInput, fmt.Sprintf(" · %d needs you", needs))
	}
	return []string{m.st.icon(state), name, title, m.st.dim("chart"), counts}
}
