package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// subRef is the row a sub-step is in its pane: the cursor stands on it, and enter
// opens the slice a slice sub-step names, else the screen of its step.
func subRef(ticket string, step int, s phase.Sub) rowRef {
	r := rowRef{kind: rowSub, id: s.Key, ticket: ticket, container: strings.ToLower(stepNames[step])}
	if id, ok := strings.CutPrefix(s.Key, "slice:"); ok {
		r.slice = id
	}
	return r
}

// substepsPanel is the pane of the sub-steps of step, height lines tall with its
// frame, titled by the step's name alone (the steps above number it): a list
// with a state, a label and a few words each, the labels in a column, every
// sub-step a row the cursor can stand on. The pane draws the window of rows
// around its cursor that its room holds and counts the rows above and below it;
// its cursor begins on the sub-step that matters now (focusIndex), which is
// opened (focus.go) when the room allows it beside the rest.
func (m *Model) substepsPanel(t *ticket, step int, subs []phase.Sub, height int) page {
	var p page
	done := 0
	labelW := 0
	for _, s := range subs {
		if s.State == phase.SubDone {
			done++
		}
		labelW = max(labelW, width(s.Label))
	}
	// The label column is as wide as its name, so the open sub-step's line and the
	// folded ones keep one column.
	labelW = max(labelW, width(subColumnNames[1]))
	title := stepNames[step]
	p.boxTop(title, fmt.Sprintf("%d of %d done", done, len(subs)))
	room := height - 2
	// The names of the columns take a line of the room when there is one to
	// spare for them and a row.
	named := room >= 3
	if named {
		room--
	}
	c := m.clocksOf(t)
	focus := focusIndex(subs)
	refs := make([]rowRef, len(subs))
	ids := make([]rowIdentity, len(subs))
	for i, s := range subs {
		refs[i] = subRef(t.id, step, s)
		ids[i] = refs[i].identity()
	}
	p.windowed = map[string][]rowRef{title: refs}
	cur := m.paneCursorIn(title, ids, homeSub(subs, focus))
	from, to, openRows, merged := m.subWindow(t, step, subs, cur, focus, room)

	var picked []int
	for i := from; i < to; i++ {
		if i != focus || openRows == 0 {
			picked = append(picked, i)
		}
	}
	rowRoom := room - openRows - markLines(from, to, len(subs), merged)
	names, laid := m.foldedFit(t, step, subs, picked, labelW, c, max(len(picked), rowRoom))
	if named {
		p.boxNames(names)
	}
	if from > 0 && !merged {
		p.boxAdd(m.st.dim("↑ " + strconv.Itoa(from) + " more"))
	}
	next := 0
	for i := from; i < to; i++ {
		at := len(p.lines)
		if i == focus && openRows > 0 {
			var lines []string
			for _, line := range m.openLines(t, step, subs[i], subs, labelW, openRows, c) {
				// The block counts a row for each of these, so each is cut to one.
				lines = append(lines, fit(line, m.innerWidth()))
			}
			p.boxLaid(&refs[i], lines)
		} else {
			p.boxLaid(&refs[i], laid[next])
			next++
		}
		if i == cur {
			p.lines[at].home = true
		}
	}
	switch {
	case merged:
		p.boxAdd(m.st.dim(fmt.Sprintf("↑ %d more · ↓ %d more", from, len(subs)-to)))
	case to < len(subs):
		p.boxAdd(m.st.dim("↓ " + strconv.Itoa(len(subs)-to) + " more"))
	}
	p.boxEnd()
	return p
}

// homeSub is the sub-step the pane's cursor begins on: the one that matters now
// (focus), else the first that is not done, else the last.
func homeSub(subs []phase.Sub, focus int) int {
	if focus >= 0 {
		return focus
	}
	for i, s := range subs {
		if s.State != phase.SubDone {
			return i
		}
	}
	return max(0, len(subs)-1)
}


// subWindow chooses the sub-steps of subs the pane draws in room lines: the rows
// from to to-1, which hold the cursor's row cur and, when the room allows, the
// one that matters now (focus), which then takes openRows lines. A window that
// does not reach an end of the list gives a line to the count of what is past
// it. The open sub-step takes what the others leave, as many rows as there are
// room for before it takes more than its three lines.
func (m *Model) subWindow(t *ticket, step int, subs []phase.Sub, cur, focus, room int) (from, to, openRows int, merged bool) {
	n := len(subs)
	if focus >= 0 {
		lo, hi := min(cur, focus), max(cur, focus)+1
		if windowCost(n, lo, hi, openMin-1, room) <= room {
			from, to, merged = windowAround(n, lo, hi, openMin-1, room)
			// The open sub-step is not worth the rows of the others: it opens when
			// it can stay beside two of them, as it always has.
			if to-from >= min(n, 3) {
				want := m.openWant(t, step, subs[focus])
				free := room - (to - from) - markLines(from, to, n, merged)
				return from, to, min(want, 1+free), merged
			}
		}
	}
	from, to, merged = windowAround(n, cur, cur+1, 0, room)
	return from, to, 0, merged
}

// foldedFit lays out the sub-steps of subs at the positions picked, in their
// order, each a line or more: the line of the columns' names laid out by the
// same call, and the lines of each. A detail wraps to as many rows as it needs; a
// block short of room asks for fewer rows of each before it gives up a row.
func (m *Model) foldedFit(t *ticket, step int, subs []phase.Sub, picked []int, labelW int, c clocks, room int) (names []string, laid [][]string) {
	cells := make([][]string, len(picked)+1)
	cells[0] = make([]string, len(subColumnNames))
	for i, n := range subColumnNames {
		cells[0][i] = m.st.dim(n)
	}
	for i, j := range picked {
		cells[i+1] = m.foldedCells(t, step, subs[j], labelW, c)
	}
	all := laidWithin(subColumns, cells, m.innerWidth(), room+1)
	return all[0], all[1:]
}
