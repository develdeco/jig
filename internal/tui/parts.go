package tui

import (
	"fmt"
	"slices"
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
	// tight says the room is the floor the screen asked for and the body has less:
	// a row cannot be read by lines in it.
	tight bool
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
		raw := m.laidList(p, s.cols, names, cells, s.room, refs, false)
		laid := raw[1:]
		from, to, cap := 0, len(laid), s.room
		if s.room > 0 && linesOf(laid) > s.room && slices.ContainsFunc(s.rows, func(r listRow) bool { return r.ref != nil && m.top().isExpanded(p.paneTitle(), r.ref) }) {
			// An expanded row never grows its pane past its room: the row the cursor
			// is on is read by lines in what the folded pane held; when it is not
			// the cursor's, or that is under three lines, the rows are folded.
			folded := m.laidList(p, s.cols, names, cells, s.room, refs, true)
			from, to, cap = m.rowsInRoom(p, s, laid, linesOf(folded[1:]))
			if to-from == len(laid) {
				raw, laid = folded, folded[1:]
			}
		}
		p.boxNames(raw[0].lines)
		alone := to-from < len(laid)
		before, after := s.before, s.after
		var marks []countMark
		if alone {
			// The row drawn alone is read by lines in the room the pane has, the
			// counts of the rows around it taking a line each when there is the
			// room for them.
			before, after = nil, nil
			if cap >= 5 {
				if from > 0 {
					marks = append(marks, countMark{"↑ " + strconv.Itoa(from) + " more", 0, from, true})
					cap--
				}
				if to < len(laid) {
					marks = append(marks, countMark{"↓ " + strconv.Itoa(len(laid)-to) + " more", to, len(laid), false})
					cap--
				}
			}
		}
		for _, l := range before {
			p.boxAdd(l)
		}
		for _, mk := range marks {
			if mk.above {
				p.lines = append(p.lines, mk.line(m, s.rows))
			}
		}
		for i := from; i < to; i++ {
			p.boxRow(s.rows[i].ref, laid[i])
		}
		for _, mk := range marks {
			if !mk.above {
				p.lines = append(p.lines, mk.line(m, s.rows))
			}
		}
		for _, l := range after {
			p.boxAdd(l)
		}
		s.after = nil
		if s.room > 0 {
			if p.rooms == nil {
				p.rooms = map[string]int{}
			}
			p.rooms[s.title] = cap
		}
	}
	for _, l := range s.after {
		p.boxAdd(l)
	}
	p.boxEnd()
}

// linesOf is the lines the rows take.
func linesOf(rows []laidRow) int {
	n := 0
	for _, r := range rows {
		n += len(r.lines)
	}
	return n
}

// countMark is the line that counts the rows of a list from lo to hi-1 a pane
// drew none of: it stands for them, so the cursor passes over them.
type countMark struct {
	text   string
	lo, hi int
	above  bool
}

func (c countMark) line(m *Model, rows []listRow) pline {
	var hidden []rowRef
	for _, r := range rows[c.lo:c.hi] {
		if r.ref != nil {
			hidden = append(hidden, *r.ref)
		}
	}
	return pline{text: m.st.dim(c.text), box: boxMid, hiddenRefs: hidden}
}

// rowsInRoom is the rows of a list the pane draws, from from to to-1, and the
// most lines one of them may take (cap). An expanded row never grows its pane:
// when the rows laid out take more lines than the room the pane was given, the
// row the cursor stands on, when it is expanded, is drawn alone with
// a count of the rows left out, and read by lines in the room that is left.
func (m *Model) rowsInRoom(p *page, s listSpec, laid []laidRow, foldedLines int) (from, to, cap int) {
	from, to, cap = 0, len(laid), s.room
	// The pane never takes more than it took folded.
	avail := min(s.room, foldedLines) + len(s.before) + len(s.after)
	if s.tight {
		avail = min(avail, 2)
	}
	if avail < 2 {
		// Under two lines a row cannot be read by lines: it stays folded.
		return from, to, cap
	}
	e := m.top()
	// Only the row the cursor stands on can be drawn alone: the cursor must stay
	// on a row the page has.
	pick := -1
	for i, r := range s.rows {
		if r.ref != nil && e.isExpanded(p.paneTitle(), r.ref) && e.paneCurs[s.title].set && e.paneCurs[s.title].id == r.ref.identity() {
			pick = i
		}
	}
	if pick < 0 {
		return from, to, cap
	}
	return pick, pick + 1, avail
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

// laidListFor lays the cells of a list out under cols with its names, in one call
// of the allocator, adds the names row to the panel p and gives back each row
// with the cut marks it carries. room, when it is more than 0, is the most lines
// the rows may take, and when it is less than 0 the rows are not cut at all.
// refs, when given, are the rows' places for the cursor, in the order of cells:
// a row the screen has expanded (expand.go) is laid out whole. A list with no
// refs is the pane's own, which is laid out whole when the pane is expanded.
func (m *Model) laidList(p *page, cols []column, names []string, cells [][]string, room int, refs []*rowRef, folded bool) []laidRow {
	all := make([][]string, 0, len(cells)+1)
	all = append(all, names)
	all = append(all, cells...)
	pane := p.paneTitle()
	whole := m.top().wholeOf(pane, 1, refs)
	if folded {
		// The rows are laid out as they are folded, whatever is expanded.
		whole = nil
	}
	if !slices.ContainsFunc(refs, func(r *rowRef) bool { return r != nil }) {
		whole = m.wholePane(pane, len(all))
		if whole != nil {
			// The names are the first row of the list and are never cut.
			whole[0] = false
		}
	} else {
		// A row the cursor cannot stand on sits under the cursor row above it: that
		// row holds its cut, and expanding it lays the two out whole. Above the
		// first row nothing holds it, and it is never cut.
		var above *rowRef
		for i, r := range refs {
			switch {
			case r != nil:
				above = r
			case above == nil || !folded && m.top().isExpanded(pane, above):
				if whole == nil {
					whole = make([]bool, len(all))
				}
				whole[1+i] = true
			}
		}
	}
	laid := m.laidOnce(cols, all, room, whole, func() []laidRow {
		switch {
		case room < 0:
			// What a person decides on is never cut: its free text wraps as far as it
			// goes.
			return layoutMarked(cols, all, m.innerWidth(), wholeRows, whole, true)
		case room > 0:
			return laidWithinMarked(cols, all, m.innerWidth(), room+1, whole, true)
		}
		return layoutMarked(cols, all, m.innerWidth(), wrapRows, whole, true)
	})
	return laid
}

// laidListFor is laidList with the names row added to the panel p and the rows
// given back without it.
func (m *Model) laidListFor(p *page, cols []column, names []string, cells [][]string, room int, refs []*rowRef) []laidRow {
	laid := m.laidList(p, cols, names, cells, room, refs, false)
	p.boxNames(laid[0].lines)
	return laid[1:]
}

// laidOnce is the layout of the cells under cols in room, with the rows whole
// says are expanded, as the allocator last gave it for the same cells at the same
// width: a frame that moves the cursor reads the same lists and measures none of
// them again. The rows it gives back are the memo's, and nothing changes them.
func (m *Model) laidOnce(cols []column, all [][]string, room int, whole []bool, lay func() []laidRow) []laidRow {
	var k strings.Builder
	fmt.Fprintf(&k, "%d|%d|%v|%v", room, m.innerWidth(), cols, whole)
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
		m.laid = map[string][]laidRow{}
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
