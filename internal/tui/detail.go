package tui

import "strconv"

// The detail part. A phase screen is a list of its things and, under the list,
// the Detail panel: the row under the cursor, whole. One function
// draws it for the four phase screens, so what a field is and how it wraps is
// said once.

// detailRow is one field of a detail: its label at the left and its text, wrapped
// whole at the room the label leaves. A row with no label goes on from the row
// above it, as the lines of a field that has several do.
type detailRow struct {
	label string
	text  string
	// lines, when it is set, are the text laid out by the caller for the room the
	// row has (detailRoom): drawn as they are, with their indents and their
	// blank lines, in place of text.
	lines []string
	// ref, when it is set, makes the row's first line a row the cursor can stand
	// on, opening what ref names (see detailSpec.walk).
	ref *rowRef
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
	// walk makes every line of the detail a row the cursor can stand on: a row
	// with a ref opens what it names, the others open nothing. The Detail is then
	// a pane with a cursor that walks down its text, and the window follows it,
	// where a Detail of text is only scrolled.
	walk bool
}

// detailLines are the lines of the Detail panel d drawn in a panel w cells wide:
// its top edge, every field wrapped whole, and its bottom edge.
func (m *Model) detailLines(d detailSpec, w int) []pline {
	inner := max(1, w-7)
	title := "Detail · " + d.title
	lines := []pline{{text: title, right: d.summary, box: boxTop}}
	// Every line carries the holder it stands for: its row, else the row above it,
	// else the panel.
	stamp := expKey{pane: title}
	labelW := 0
	for _, r := range d.rows {
		labelW = max(labelW, width(r.label))
	}
	n := 0
	for _, r := range d.rows {
		ask := d.ask
		if r.apart {
			ask = ""
		}
		for i, text := range m.detailRowLines(r, labelW, inner) {
			l := pline{text: text, box: boxMid, ask: ask}
			switch {
			case r.ref != nil && i == 0:
				ref := *r.ref
				l.ref = &ref
			case d.walk:
				l.ref = &rowRef{kind: rowStop, id: "line-" + strconv.Itoa(n)}
			}
			if l.ref != nil {
				stamp = expKey{pane: title, id: l.ref.identity()}
			}
			l.holder, l.held = stamp, true
			n++
			lines = append(lines, l)
		}
	}
	if len(d.rows) == 0 && d.empty != "" {
		lines = append(lines, pline{text: m.st.dim(d.empty), box: boxMid, holder: stamp, held: true})
	}
	return append(lines, pline{box: boxBottom})
}

// detailRoom is the cells a row's text has in a Detail panel drawn across the
// screen, when its label is labelW wide: what detailRowLines wraps to.
func (m *Model) detailRoom(labelW int) int {
	return max(1, max(1, m.width-7)-labelW-1)
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
	laid := r.lines
	if laid == nil {
		laid = wrapWhole(r.text, room)
	}
	for i, line := range laid {
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
	collapsed  = 2
	// detailLeast is the least a Detail panel is drawn in: its edges, a first row
	// and a mark.
	detailLeast = 4
)

// listAndDetail adds the list panels and, with them, the Detail panel of the row
// the cursor is on, at the place at among the rows of the lists in order, under
// the lists at every width. The lists and the detail share the room the screen leaves them: what
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
	if m.top().zoom != "" {
		m.zoomedBlock(p, d, at, lists)
		return
	}
	first := m.taken(p) // the lines above are wrapped as they will be drawn
	full := make([][]pline, len(lists))
	total := 0
	for i, l := range lists {
		var q page
		m.listPanel(&q, l)
		full[i] = q.lines
		total += len(q.lines)
	}
	// The cursor's list and its place in it.
	cl, cr := 0, at
	for i, l := range lists {
		if cr < len(l.rows) {
			cl = i
			break
		}
		cr -= len(l.rows)
		cl = i + 1
	}
	// The least a block of lists and detail is: the cursor's list, with its row as
	// tall as it is, the others as a mark each, and the detail's edges, a first row
	// and a mark. When the lines above leave it less than that, it has the window
	// but for the marks of what lies above and below, and the window starts at it
	// (blockTop).
	listsLeast := listMin + rowExtra(full, cl, cr) + collapsed*(len(lists)-1)
	least := listsLeast + 4
	room := m.bodyRows() - 1 // the mark of what lies below
	avail := room - first
	shifted := avail < least
	if shifted {
		floor := listsLeast + 1 // and the line that says the detail has no room
		avail = max(room-1, floor)
	}
	dl := m.detailLines(*d, m.width)

	// The room each takes: the detail has what the lists leave, at least its peek
	// (the whole of an ask's, which is decided from, while the lists keep their
	// minimum).
	lb, db := total, len(dl)
	if total+len(dl) > avail {
		need := min(len(dl), detailPeek)
		if d.ask != "" {
			need = len(dl)
		}
		db = max(min(len(dl), avail-total), need)
		db = max(min(db, avail-listMin), min(len(dl), detailPeek))
		lb = avail - db
		if db < min(len(dl), detailLeast) {
			lb = avail - 1 // the line that says the detail has no room
		}
	}

	// The lists, each windowed around the cursor's row when it is in it, the
	// others to what they need up to a few lines.
	var left []pline
	if lb >= total {
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
		row := 0
		if lb-others < listMin {
			keep, others, row = collapsed, 0, -1
			for i, f := range full {
				if i != cl {
					others += min(len(f), keep)
				}
			}
		}
		budget := make([]int, len(full))
		for i := range full {
			budget[i] = keep
		}
		budget[cl] = max(lb-others, listMin)
		cut := make([][]pline, len(full))
		draw := func(i int) {
			at := row
			if i == cl {
				at = cr
			}
			cut[i] = m.windowList(full[i], at, budget[i])
		}
		size := 0
		for i := range full {
			draw(i)
			size += len(cut[i])
		}
		// The lines the cursor's list does not use go to the other lists, which draw
		// more of their rows with them.
		for i := range full {
			was := len(cut[i])
			for b := budget[i] + 1; i != cl && b <= budget[i]+lb-size && was < len(full[i]); b++ {
				if got := m.windowList(full[i], 0, b); len(got) > was && size+len(got)-was <= lb {
					size += len(got) - was
					cut[i], budget[i] = got, b
					break
				}
			}
		}
		for i := range full {
			left = append(left, cut[i]...)
		}
	}
	right, cut := cutDetail(m, dl, db)
	if spare := avail - len(left); spare < min(len(dl), detailLeast) {
		// What the lists leave is less than the detail's least: one line says so,
		// in the detail's place.
		right, cut = []pline{{text: m.st.dim(partNeeds("Detail", min(len(dl), detailLeast), spare)), ask: d.ask}}, true
	} else if len(left) > lb {
		right, cut = cutDetail(m, dl, spare)
	}
	if cut && d.ask != "" {
		if p.cutAsks == nil {
			p.cutAsks = map[string]bool{}
		}
		p.cutAsks[d.ask] = true
	}
	top := len(p.lines)
	p.lines = append(p.lines, left...)
	detailAt := len(p.lines)
	p.lines = append(p.lines, right...)
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
		// A list with no room: its two edges, the count its title says, and its rows
		// kept in their places on the bottom edge.
		var all []rowRef
		for _, s := range starts {
			all = append(all, *body[s].ref)
		}
		bottom := f[len(f)-1]
		bottom.hiddenRefs = all
		return []pline{f[0], bottom}
	}
	row = max(0, min(row, len(starts)-1))
	lo, hi := row, row
	kept := head // the lines above the rows that stay: the edge and the names
	need := func(lo, hi int) int {
		n := kept + 1 + end(hi) - starts[lo]
		if lo > 0 {
			n++
		}
		if hi < len(starts)-1 {
			n++
		}
		return n
	}
	if need(row, row) > budget {
		kept = 1 // no room for the names: the edge, the marks and the row
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
	out := append([]pline(nil), f[:kept]...)
	if lo > 0 {
		out = append(out, pline{text: m.st.dim("↑ " + strconv.Itoa(lo) + " more"), box: boxMid, hiddenRefs: refs(0, lo)})
	}
	out = append(out, body[starts[lo]:end(hi)]...)
	if hi < len(starts)-1 {
		out = append(out, pline{text: m.st.dim("↓ " + strconv.Itoa(len(starts)-1-hi) + " more"), box: boxMid, hiddenRefs: refs(hi+1, len(starts))})
	}
	return append(out, f[len(f)-1])
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

// rowExtra is how many lines the cursor's row of the list cl has beyond its
// first: a row that wraps needs them as well as the list's minimum.
func rowExtra(full [][]pline, cl, cr int) int {
	if cl >= len(full) {
		return 0
	}
	n, in := 0, false
	for _, l := range full[cl] {
		switch {
		case l.ref != nil:
			if in {
				return n
			}
			in = cr == 0
			cr--
		case in && l.cont:
			n++
		case in:
			return n
		}
	}
	return n
}

// zoomedBlock is the lists and the Detail of a screen with one pane zoomed: the
// pane has the whole body, a list windowed around its cursor's row and a Detail
// whole, and the others are only what the keys count over.
func (m *Model) zoomedBlock(p *page, d *detailSpec, at int, lists []listSpec) {
	z := m.top().zoom
	cl, cr := 0, at
	for i, l := range lists {
		if cr < len(l.rows) {
			cl = i
			break
		}
		cr -= len(l.rows)
		cl = i + 1
	}
	for i, l := range lists {
		var q page
		m.listPanel(&q, l)
		switch {
		case l.title == z && i == cl:
			p.lines = append(p.lines, m.windowList(q.lines, cr, m.bodyRows())...)
		case l.title == z:
			p.lines = append(p.lines, m.windowList(q.lines, 0, m.bodyRows())...)
		default:
			p.lines = append(p.lines, m.windowList(q.lines, -1, collapsed)...)
		}
	}
	dl := m.detailLines(*d, m.width)
	if z != dl[0].text {
		dl, _ = cutDetail(m, dl, detailLeast)
	}
	detailAt := len(p.lines)
	p.lines = append(p.lines, dl...)
	p.lines[detailAt].hold |= holdFrom
}
