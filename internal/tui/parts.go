package tui

import (
	"strconv"
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
	// before are lines inside the panel over the rows, under the names, such as
	// the count of the newer ones; after are lines under the rows, such as the
	// count of the older ones.
	before, after []string
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
		refs := make([]*rowRef, len(s.rows))
		for i, r := range s.rows {
			cells[i], refs[i] = r.cells, r.ref
		}
		laid := m.laidListFor(p, s.cols, names, cells, s.room, refs)
		for _, l := range s.before {
			p.boxAdd(l)
		}
		for i, row := range laid {
			p.boxRow(s.rows[i].ref, row)
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
	laid := m.laidListFor(p, cols, names, cells, room, nil)
	out := make([][]string, len(laid))
	for i, r := range laid {
		out[i] = r.lines
	}
	return out
}

// laidListFor is laidList that gives each row with the cut marks it carries.
// refs, when given, are the rows' places for the cursor, in the order of cells:
// a row the screen has expanded (expand.go) is laid out whole.
func (m *Model) laidListFor(p *page, cols []column, names []string, cells [][]string, room int, refs []*rowRef) []laidRow {
	all := make([][]string, 0, len(cells)+1)
	all = append(all, names)
	all = append(all, cells...)
	whole := m.top().wholeOf(p.paneTitle(), 1, refs)
	var laid []laidRow
	switch {
	case room < 0:
		// What a person decides on is never cut: its free text wraps as far as it
		// goes.
		laid = layoutMarked(cols, all, m.innerWidth(), wholeRows, whole)
	case room > 0:
		laid = laidWithinMarked(cols, all, m.innerWidth(), room+1, whole)
	default:
		laid = layoutMarked(cols, all, m.innerWidth(), wrapRows, whole)
	}
	p.boxNames(laid[0].lines)
	return laid[1:]
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
