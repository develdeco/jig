package tui

import "fmt"

// chartColumns are the columns of the chart rows of the project's list, which
// follow the tickets' (idW, titleW) and widen for a longer name or title, as far
// as the row has room.
func (m *Model) chartColumns(names []string, idW, titleW int) (nameW, outTitleW int) {
	nameW, ownW := idW, 0
	for _, name := range names {
		nameW = max(nameW, width(name))
		ownW = max(ownW, width(m.w.charts[name].title()))
	}
	// The room left once the icon, the name, "chart" and the counts are drawn.
	return nameW, max(titleW, min(ownW, max(0, m.width-nameW-42)))
}

// chartMainRow is the chart's row on the project's list: how many of its
// tickets are published and whether one needs the person.
func (m *Model) chartMainRow(name string, nameW, titleW int) string {
	c := m.w.charts[name]
	rows := m.chartRows(c)
	total, done, needs := chartProgress(rows)
	state, _ := m.chartStatus(c, rows)
	title := c.title()
	if title == name {
		title = ""
	}
	line := m.st.icon(state) + " " + pad(name, nameW)
	if titleW > 0 {
		line += "  " + pad(title, titleW)
	}
	line += "  " + m.st.dim("chart") + "  "
	if total == 0 {
		line += m.st.dim("not graduated")
	} else {
		line += m.st.dim(fmt.Sprintf("%d of %d published", done, total))
	}
	if needs > 0 {
		line += m.st.state(StateNeedsInput, fmt.Sprintf(" · %d needs you", needs))
	}
	return line
}
