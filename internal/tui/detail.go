package tui

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

// listAndDetail adds the list panels and, with them, the Detail panel of the row
// the cursor is on: under the lists, or from sideMinWidth columns beside them,
// both boxes as tall as the taller. A screen with no row to detail passes none.
func (m *Model) listAndDetail(p *page, d *detailSpec, lists ...listSpec) {
	if d == nil {
		for _, l := range lists {
			m.listPanel(p, l)
		}
		return
	}
	first := len(p.lines)
	if m.width < sideMinWidth {
		// The lists are laid out as they are asked to be. When what is above the
		// detail leaves it no room, the rows of a list that can give up lines
		// take one line each: the detail has each field whole.
		var list page
		for pass := 0; pass < 2; pass++ {
			list = page{}
			for _, l := range lists {
				if pass == 1 && l.room == 0 {
					l.room = len(l.rows)
				}
				m.listPanel(&list, l)
			}
			if first+len(list.lines)+detailPeek <= m.bodyRows() {
				break
			}
		}
		p.lines = append(p.lines, list.lines...)
		first = len(p.lines)
		p.lines = append(p.lines, m.detailLines(*d, m.width)...)
	} else {
		listW := m.width * 3 / 5
		sideW := m.width - listW
		var left page
		m.within(listW, func() {
			for _, l := range lists {
				m.listPanel(&left, l)
			}
		})
		p.lines = append(p.lines, m.zipSides(left.lines, m.detailLines(*d, sideW), sideW)...)
	}
	p.lines[first].hold |= holdFrom
	p.lines[len(p.lines)-1].hold |= holdTo
}

// The marks on the first and the last line of a Detail panel (pline.hold).
const (
	holdFrom = 1 << iota
	holdTo
)

// detailPeek is the lines of a Detail panel a window must reach when the page
// begins at its top for the list above it to leave the detail room: its top
// edge, two rows of it, the mark of what is cut and its bottom edge.
const detailPeek = 5

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
