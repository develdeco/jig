package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The parts every screen draws with. A section of a screen is a panel; a panel
// of rows that are cells in the allocator is a list and names its columns in a
// dim row under its top edge, laid out by the same call as its rows.

// listRow is a row of a list: the cells of its columns and the place the
// cursor takes when it can stand on the row.
type listRow struct {
	ref   *rowRef
	cells []string
	// notes are lines under the row that belong to it: the cursor on the row is on
	// them too, and a window never cuts them from it.
	notes []string
}

// listSpec is a list panel.
type listSpec struct {
	title, summary string
	cols           []column
	// names are the words of the columns, a lowercase word or two each, in the
	// order of cols; a column that answers nothing has an empty name.
	names []string
	rows  []listRow
	// empty is said in place of the list when it has no rows, and the panel then
	// has no names row.
	empty string
	// room is the most lines the rows may take, the names row apart; 0 is no
	// cap. A list short of room asks its free text for fewer rows before it
	// drops one.
	room int
	// after are lines inside the panel under the rows, such as the count of
	// the older ones.
	after []string
}

// listPanel adds a list panel to p: the top edge with the title and the summary,
// the names of the columns, the rows, and the bottom edge.
func (m *Model) listPanel(p *page, s listSpec) {
	p.boxTop(s.title, s.summary)
	if len(s.rows) == 0 {
		if s.empty != "" {
			p.boxAdd(m.st.dim(s.empty))
		}
	} else {
		names := make([]string, len(s.names))
		for i, n := range s.names {
			names[i] = m.st.dim(n)
		}
		cells := make([][]string, len(s.rows))
		for i, r := range s.rows {
			cells[i] = r.cells
		}
		for i, lines := range m.laidList(p, s.cols, names, cells, s.room) {
			p.boxLaid(s.rows[i].ref, lines)
			for _, note := range s.rows[i].notes {
				p.lines = append(p.lines, pline{text: note, box: boxMid, cont: true})
			}
		}
	}
	for _, l := range s.after {
		p.boxAdd(l)
	}
	p.boxEnd()
}

// ageOf is how long ago at was, in its largest whole unit: "42s", "4m", "3h",
// "13d". It is empty for a row with no line yet.
func (m *Model) ageOf(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := max(0, m.now().Sub(at))
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// laidList lays the cells of a list out under cols with its names, in one call of
// the allocator, adds the names row to the panel p and gives back the lines of
// each row. room, when it is more than 0, is the most lines the rows may take,
// and when it is less than 0 the rows are not cut at all.
func (m *Model) laidList(p *page, cols []column, names []string, cells [][]string, room int) [][]string {
	all := make([][]string, 0, len(cells)+1)
	all = append(all, names)
	all = append(all, cells...)
	laid := m.laidOnce(cols, all, room, func() [][]string {
		switch {
		case room == oneRow:
			// A list that gives up what it can for room: each row is one line, and
			// the detail beside or under it has the text whole.
			return layoutRows(cols, all, m.innerWidth(), 1)
		case room < 0:
			// What a person decides on is never cut: its free text wraps as far as it
			// goes.
			return layoutRows(cols, all, m.innerWidth(), 1<<20)
		case room > 0:
			return laidWithin(cols, all, m.innerWidth(), room+1)
		}
		return layout(cols, all, m.innerWidth())
	})
	p.boxNames(laid[0])
	return laid[1:]
}

// oneRow is the room of a list whose rows are one line each.
const oneRow = -2

// laidOnce is the layout of the cells under cols in room, as the allocator last
// gave it for the same cells at the same width: a frame that moves the cursor
// reads the same lists and measures none of them again. The lines it gives back
// are the memo's, and nothing changes them.
func (m *Model) laidOnce(cols []column, all [][]string, room int, lay func() [][]string) [][]string {
	var k strings.Builder
	fmt.Fprintf(&k, "%d|%d|%v", room, m.innerWidth(), cols)
	for _, row := range all {
		for _, c := range row {
			k.WriteString(c)
			k.WriteByte(0)
		}
		k.WriteByte(1)
	}
	key := k.String()
	if laid, ok := m.laid[key]; ok {
		return laid
	}
	if m.laid == nil || len(m.laid) > 128 {
		m.laid = map[string][][]string{}
	}
	m.laidBuilds++
	laid := lay()
	m.laid[key] = laid
	return laid
}

// boxNames adds the row of a list's column names to the panel.
func (p *page) boxNames(lines []string) {
	for i, l := range lines {
		p.lines = append(p.lines, pline{text: l, box: boxMid, names: true, cont: i > 0})
	}
}

// intoPanel makes the lines the page holds from line at the lines of a panel
// titled title, with summary at the right of its top edge: a section built as
// loose rows becomes a panel.
func (p *page) intoPanel(at int, title, summary string) {
	body := append([]pline(nil), p.lines[at:]...)
	p.lines = p.lines[:at]
	p.boxTop(title, summary)
	for _, l := range body {
		l.box = boxMid
		p.lines = append(p.lines, l)
	}
	p.boxEnd()
}
