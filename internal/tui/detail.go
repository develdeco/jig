package tui

import "strconv"

// The detail part. A phase screen is a list of its things and, under the list or
// beside it, the Detail panel: the row under the cursor, whole. One function
// draws it for the four phase screens, so what a field is and how it wraps is
// said once.

// sideMinWidth is the width from which the detail sits beside its list, the list
// taking three fifths of the width and the detail the rest.
const sideMinWidth = 160

// detailRow is one field of a detail: its label at the left and its text, wrapped
// whole at the room the label leaves. A row with no label goes on from the row
// above it, as the lines of a field that has several do.
type detailRow struct {
	label string
	text  string
	// icon, when it is set, is the state drawn before the text; the lines the
	// text wraps to stand under it.
	icon string
	// state draws the text in that state's color, dim draws it back.
	state string
	dim   bool
	// indent is cells the whole row stands in, for a line that belongs to the
	// one above it.
	indent int
	// apart is a row that is not part of the text an ask is decided from (the
	// decision so far): the window may cut it and the ask is still drawn whole.
	apart bool
}

// detailSpec is a Detail panel: the title is the row's own id or name, drawn after
// the panel's name (a panel's title has a capital), the summary
// what the row stands at.
type detailSpec struct {
	title, summary string
	rows           []detailRow
	// empty is said when there are no rows.
	empty string
	// ask, when it is set, is the ask the detail is of: every line of it carries
	// the ask, so the window can tell whether it was drawn whole.
	ask string
}

// within runs build with panels drawn w cells wide, as the side of a screen is.
func (m *Model) within(w int, build func()) {
	was := m.panelW
	m.panelW = w
	build()
	m.panelW = was
}

// detailLines are the lines of the Detail panel d drawn in a panel w cells wide:
// its top edge, every field wrapped whole, and its bottom edge.
func (m *Model) detailLines(d detailSpec, w int) []pline {
	inner := max(1, w-7)
	lines := []pline{{text: "Detail · " + d.title, right: d.summary, box: boxTop}}
	labelW := 0
	for _, r := range d.rows {
		labelW = max(labelW, width(r.label))
	}
	for _, r := range d.rows {
		ask := d.ask
		if r.apart {
			ask = ""
		}
		for _, text := range m.detailRowLines(r, labelW, inner) {
			lines = append(lines, pline{text: text, box: boxMid, ask: ask})
		}
	}
	if len(d.rows) == 0 && d.empty != "" {
		lines = append(lines, pline{text: m.st.dim(d.empty), box: boxMid})
	}
	return append(lines, pline{box: boxBottom})
}

// detailRowLines is a field as lines, its text broken at words into the room
// the label, the icon and the indent leave and none of it cut.
func (m *Model) detailRowLines(r detailRow, labelW, inner int) []string {
	lead := spaces(r.indent)
	room := inner - labelW - 1 - r.indent
	if labelW == 0 {
		room = inner - r.indent
	}
	mark := 0
	if r.icon != "" {
		mark = 2
	}
	room = max(1, room-mark)
	var out []string
	for i, line := range wrapWhole(r.text, room) {
		switch {
		case r.state != "":
			line = m.st.state(r.state, line)
		case r.dim:
			line = m.st.dim(line)
		}
		head := ""
		if labelW > 0 {
			head = spaces(labelW + 1)
			if i == 0 {
				head = m.st.dim(pad(r.label, labelW)) + " "
			}
		}
		icon := spaces(mark)
		if i == 0 && r.icon != "" {
			icon = m.st.icon(r.icon) + " "
		}
		out = append(out, head+lead+icon+line)
	}
	if len(out) == 0 {
		head := ""
		if labelW > 0 {
			head = m.st.dim(pad(r.label, labelW))
		}
		out = append(out, head)
	}
	return out
}

// holdFrom marks the first line of a Detail panel (pline.hold): the page is read
// with the list and its detail together.
const (
	holdFrom  = 1
	holdBlock = 2 // the first line of a block the window starts at
)

// detailPeek is the least lines of a Detail panel the lists leave it: its top
// edge, two rows, the mark of what is cut and its bottom edge. listMin is the
// least a list keeps when the detail takes room from it: its edges, its names,
// the cursor's row and the marks of what is hidden.
const (
	detailPeek = 5
	listMin    = 5
	collapsed  = 3
	// tightLeast is the least a block can be when the lists give way: the cursor's
	// list as three lines and the detail's four.
	tightLeast = 7
)

// listAndDetail adds the list panels and, with them, the Detail panel of the row
// the cursor is on, at the place at among the rows of the lists in order: under
// the lists, or from sideMinWidth columns beside them, both boxes as tall as the
// taller. The lists and the detail share the room the screen leaves them: what
// does not fit is cut where it is, a list around its cursor's row and a detail at
// its end, each with a mark of what it hides inside its own box, so the detail is
// always drawn. A screen with no row to detail passes none.
func (m *Model) listAndDetail(p *page, d *detailSpec, at int, lists ...listSpec) {
	if d == nil {
		for _, l := range lists {
			m.listPanel(p, l)
		}
		return
	}
	first := m.taken(p) // the lines above are wrapped as they will be drawn
	side := m.width >= sideMinWidth
	// The least a block of lists and detail is: the cursor's list, the others as a
	// mark each, and the detail's edges, a first row and a mark. When the lines
	// above leave it less than that, it has the window but for the marks of what
	// lies above and below, and the window starts at it (blockTop).
	least := listMin + collapsed*(len(lists)-1) + 4
	if side {
		least = max(listMin+collapsed*(len(lists)-1), 5)
	}
	room := m.bodyRows() - 1 // the mark of what lies below
	avail := room - first
	shifted, tight := false, false
	if avail < least {
		switch {
		case room-1 >= least:
			shifted, avail = true, room-1
		case !side:
			// Too short for all of it: the lists the cursor is not in give way
			// altogether, and its own is its row between its edges.
			tight = true
			if avail < tightLeast {
				shifted, avail = true, max(room-1, tightLeast)
			}
		default:
			shifted, avail = true, max(room-1, least)
		}
	}
	listW, sideW := m.width, m.width
	if side {
		listW = m.width * 3 / 5
		sideW = m.width - listW
	}
	full := make([][]pline, len(lists))
	total := 0
	m.within(listW, func() {
		for i, l := range lists {
			var q page
			m.listPanel(&q, l)
			full[i] = q.lines
			total += len(q.lines)
		}
	})
	dl := m.detailLines(*d, sideW)

	// The room each takes. Beside each other both have all of it; under it the
	// detail has what the lists leave, at least its peek (the whole of an ask's,
	// which is decided from, while the lists keep their minimum).
	lb, db := avail, avail
	if !side {
		lb, db = total, len(dl)
		if total+len(dl) > avail {
			need := min(len(dl), detailPeek)
			if d.ask != "" {
				need = len(dl)
			}
			db = max(min(len(dl), avail-total), need)
			db = max(min(db, avail-listMin), min(len(dl), detailPeek))
			lb = avail - db
		}
	}

	// The lists, each windowed around the cursor's row when it is in it, the
	// others to what they need up to a few lines.
	cl, cr := 0, at
	for i, l := range lists {
		if cr < len(l.rows) {
			cl = i
			break
		}
		cr -= len(l.rows)
		cl = i + 1
	}
	var left []pline
	if tight {
		var pending []rowRef
		for i, f := range full {
			switch {
			case i == cl:
				ls := windowTight(f, cr)
				ls[0].hiddenRefs = append(pending, ls[0].hiddenRefs...)
				pending = nil
				left = append(left, ls...)
			default:
				for _, l := range f {
					if l.ref != nil {
						pending = append(pending, *l.ref)
					}
				}
			}
		}
		if len(pending) > 0 {
			last := &left[len(left)-1]
			last.hiddenRefs = append(last.hiddenRefs, pending...)
		}
	} else if lb >= total {
		for _, f := range full {
			left = append(left, f...)
		}
	} else {
		// A list the cursor is not in keeps what it needs up to a few lines, and
		// when that leaves the cursor's list less than its minimum it is a mark.
		keep, others := detailPeek, 0
		for i, f := range full {
			if i != cl {
				others += min(len(f), keep)
			}
		}
		if lb-others < listMin {
			keep, others = 0, 0
			for i, f := range full {
				if i != cl {
					others += min(len(f), collapsed)
				}
			}
		}
		for i, f := range full {
			switch {
			case i == cl:
				left = append(left, m.windowList(f, cr, max(lb-others, listMin))...)
			case keep > 0:
				left = append(left, m.windowList(f, 0, keep)...)
			default:
				left = append(left, m.windowList(f, -1, collapsed)...)
			}
		}
	}
	if !side && (tight || len(left) > lb) {
		// The lists could not give up as much as they were asked to: the detail
		// has what is left, its edges and a mark at least.
		db = max(avail-len(left), 4)
	}
	right, cut := cutDetail(m, dl, db)
	if cut && d.ask != "" {
		if p.cutAsks == nil {
			p.cutAsks = map[string]bool{}
		}
		p.cutAsks[d.ask] = true
	}
	top := len(p.lines)
	detailAt := top
	if side {
		p.lines = append(p.lines, m.zipSides(left, right, sideW)...)
	} else {
		p.lines = append(p.lines, left...)
		detailAt = len(p.lines)
		p.lines = append(p.lines, right...)
	}
	p.lines[detailAt].hold |= holdFrom
	if shifted {
		p.lines[top].hold |= holdBlock
	}
}

// windowList is the lines of a list panel f cut to budget lines around the row
// at row, as many rows as fit with the marks of those above and below inside its
// box. The rows a mark stands for keep their places among the page's rows. A list
// that fits, or has no row, is as it is.
func (m *Model) windowList(f []pline, row, budget int) []pline {
	if len(f) <= budget || len(f) < 3 {
		return f
	}
	head := 1
	for head < len(f)-1 && f[head].names {
		head++
	}
	body := f[head : len(f)-1]
	var starts []int
	for i, l := range body {
		if l.ref != nil {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return f
	}
	end := func(k int) int { // the line after row k
		if k+1 < len(starts) {
			return starts[k+1]
		}
		return len(body)
	}
	if row < 0 {
		// A list with no room: its edges and one mark for all its rows.
		var all []rowRef
		for _, s := range starts {
			all = append(all, *body[s].ref)
		}
		mark := pline{text: m.st.dim(strconv.Itoa(len(all)) + " " + plural(len(all), "row", "rows")), box: boxMid, hiddenRefs: all}
		return []pline{f[0], mark, f[len(f)-1]}
	}
	row = max(0, min(row, len(starts)-1))
	lo, hi := row, row
	need := func(lo, hi int) int {
		n := head + 1 + end(hi) - starts[lo]
		if lo > 0 {
			n++
		}
		if hi < len(starts)-1 {
			n++
		}
		return n
	}
	for grew := true; grew; {
		grew = false
		if hi+1 < len(starts) && need(lo, hi+1) <= budget {
			hi++
			grew = true
		}
		if lo > 0 && need(lo-1, hi) <= budget {
			lo--
			grew = true
		}
	}
	refs := func(a, b int) []rowRef {
		var out []rowRef
		for k := a; k < b; k++ {
			out = append(out, *body[starts[k]].ref)
		}
		return out
	}
	out := append([]pline(nil), f[:head]...)
	if lo > 0 {
		out = append(out, pline{text: m.st.dim("↑ " + strconv.Itoa(lo) + " more"), box: boxMid, hiddenRefs: refs(0, lo)})
	}
	out = append(out, body[starts[lo]:end(hi)]...)
	if hi < len(starts)-1 {
		out = append(out, pline{text: m.st.dim("↓ " + strconv.Itoa(len(starts)-1-hi) + " more"), box: boxMid, hiddenRefs: refs(hi+1, len(starts))})
	}
	return append(out, f[len(f)-1])
}

// windowTight is a list panel f as its edges and the lines of the row at row and
// nothing else: the rows it hides stand in its edges, those above in the top one
// and those below in the bottom one, so the cursor still counts them.
func windowTight(f []pline, row int) []pline {
	head := 1
	for head < len(f)-1 && f[head].names {
		head++
	}
	body := f[head : len(f)-1]
	var starts []int
	for i, l := range body {
		if l.ref != nil {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return []pline{f[0], f[len(f)-1]}
	}
	row = max(0, min(row, len(starts)-1))
	end := len(body)
	if row+1 < len(starts) {
		end = starts[row+1]
	}
	top, bottom := f[0], f[len(f)-1]
	for k, s := range starts {
		switch {
		case k < row:
			top.hiddenRefs = append(top.hiddenRefs, *body[s].ref)
		case k > row:
			bottom.hiddenRefs = append(bottom.hiddenRefs, *body[s].ref)
		}
	}
	out := []pline{top}
	out = append(out, body[starts[row]:end]...)
	return append(out, bottom)
}

// cutDetail is the lines of a Detail panel cut to budget lines: its top edge, as
// many rows as fit, a mark of how many it hides and its bottom edge. It says
// whether it cut.
func cutDetail(m *Model, dl []pline, budget int) ([]pline, bool) {
	if len(dl) <= budget {
		return dl, false
	}
	keep := max(budget-3, 0)
	rows := dl[1 : len(dl)-1]
	if keep > len(rows) {
		keep = len(rows)
	}
	out := append([]pline(nil), dl[:1]...)
	out = append(out, rows[:keep]...)
	mark := pline{text: m.st.dim("↓ " + strconv.Itoa(len(rows)-keep) + " more"), box: boxMid}
	if len(rows) > 0 {
		mark.ask = rows[0].ask // an ask cut is an ask on the page, drawn cut
	}
	out = append(out, mark)
	return append(out, dl[len(dl)-1]), true
}

// zipSides puts the lines of a panel drawn in right beside the lines of left,
// line by line: the shorter of the two is given blank rows inside its frame,
// above its bottom edge, and each of the left's lines carries its side, so the
// window cuts the two together and the cursor's row stays on its line.
func (m *Model) zipSides(left, right []pline, sideW int) []pline {
	left, right = padPanel(left, len(right)), padPanel(right, len(left))
	out := make([]pline, len(left))
	for i, l := range left {
		l.side = m.frameLine(right[i], "  ", sideW, false)
		l.sideW = sideW
		l.ask = right[i].ask
		out[i] = l
	}
	return out
}

// padPanel is the lines of a panel with blank rows inside its frame, above its
// bottom edge, to n lines.
func padPanel(lines []pline, n int) []pline {
	if len(lines) >= n || len(lines) == 0 {
		return lines
	}
	last := len(lines) - 1
	out := append([]pline(nil), lines[:last]...)
	for len(out) < n-1 {
		out = append(out, pline{box: boxMid})
	}
	return append(out, lines[last])
}

// blankSide is a row of a side panel with nothing in it, the one a mark that the
// window cut a panel is drawn beside.
func (m *Model) blankSide(w int) string {
	return m.frameLine(pline{box: boxMid}, "  ", w, false)
}
