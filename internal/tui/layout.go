package tui

import (
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The one place a list's text is given its width. A screen decides what each
// cell says; the allocator decides what a column is as wide as, so no id, title
// or tag is cut by a number a screen chose.
const (
	// freeMin is the least a free column is given before the measured columns
	// give way to it.
	freeMin = 20
	// freeLow is the least a free column is given before an id is cut, in a list
	// whose names are kept whole.
	freeLow = 12
	// wrapRows is the most rows a free cell takes; text that goes on past them
	// ends in an ellipsis on the last.
	wrapRows = 3
	// nonBreaking stands for a hyphen while text is wrapped.
	nonBreaking = "‑"
)

// column says how one column of a list is sized.
//
// A measured column (an icon, an id, a tag, a clock, a state word) is as wide
// as its widest cell. A free column (a title, a detail, a reason: any
// sentence) shares what the measured columns leave, and wraps at words.
type column struct {
	free bool
	// id marks a column that is cut after every other measured column is.
	id bool
	// keep marks a measured column that is never cut, a clock that must always
	// show: the words give way to it, and never the reverse.
	keep bool
	// gap is the spaces drawn before the column, unless it is the first one
	// drawn.
	gap int
}

// wholeRows is how many rows a cell of an expanded row may take: no limit a row
// can reach.
const wholeRows = 1 << 20

// cutMark is the cut a laid-out line carries: the cell its "…" sits on within
// the line's text, and the whole value the cut text stands for. Only a cut the
// allocator made registers one; a word that has its own "…" is no mark.
type cutMark struct {
	x     int
	value string
}

// laidRow is a row as the allocator laid it out: its lines, and the cut marks
// each line carries (cuts[i] belongs to lines[i]).
type laidRow struct {
	lines []string
	cuts  [][]cutMark
}

// layoutMarked draws rows of cells under cols in width cells. Each row comes
// back as the lines it takes: its first line, and the continuation rows of a
// free cell that wrapped, aligned under their column, with the cut marks of
// each line. A column no row has a cell for is not drawn. Cells may carry
// color; it is kept on every line a cell wraps to.
//
// Measured columns are as wide as their widest cell. They are cut only when
// they and freeMin cells for the free text leave no room: then the widest one
// that is not an id gives way first, each ending in an ellipsis, and ids last.
//
// Free columns share the rest. When any free column would get fewer than
// freeMin cells it needs, the free columns after the first stack under the
// first's left edge instead of sitting beside it. They are held to at most
// maxRows rows each: a list short of room asks for fewer than wrapRows before
// it drops a row.
//
// A row whole says is laid out whole: every cell of it that would be cut wraps
// at words in its own column with no limit on rows, a word wider than the column
// broken at it. The columns are the widths the folded rows give them, so
// expanding a row moves no other cell of any row.
func layoutMarked(cols []column, rows [][]string, width, maxRows int, whole []bool, named bool) []laidRow {
	natural := make([]int, len(cols))
	for _, r := range rows {
		for c := range cols {
			if c < len(r) {
				natural[c] = max(natural[c], width0(r[c]))
			}
		}
	}
	var visible, free []int
	for c := range cols {
		if natural[c] == 0 {
			continue
		}
		visible = append(visible, c)
		if cols[c].free {
			free = append(free, c)
		}
	}
	w := make([]int, len(cols))
	for _, c := range visible {
		w[c] = natural[c]
	}

	stacked := map[int]bool{}
	gaps := func() int {
		g, first := 0, true
		for _, c := range visible {
			if stacked[c] {
				continue
			}
			if !first {
				g += cols[c].gap
			}
			first = false
		}
		return g
	}
	measured := func() int {
		n := 0
		for _, c := range visible {
			if !cols[c].free {
				n += w[c]
			}
		}
		return n
	}
	active := func() []int {
		var out []int
		for _, c := range free {
			if !stacked[c] {
				out = append(out, c)
			}
		}
		return out
	}

	if len(free) > 1 {
		got := share(width-gaps()-measured(), natural, free)
		for i := range free {
			if got[i] < min(natural[free[i]], freeMin) {
				for _, o := range free[1:] {
					stacked[o] = true
				}
				break
			}
		}
	}

	// A list with names never cuts them: a measured column is as wide as its
	// names, whatever it gives up.
	floor := func(c int) int {
		if named && len(rows) > 0 && c < len(rows[0]) {
			return width0(rows[0][c])
		}
		return 0
	}
	// Measured columns give way only when 20 cells for the free text do not
	// fit beside them.
	need := 0
	for _, c := range active() {
		need += min(natural[c], freeMin)
	}
	// With the names kept whole the other measured columns give less, so before
	// an id is cut the free text gives up some of its cells, down to freeLow.
	squeeze := 0
	if named {
		for _, c := range active() {
			squeeze += max(0, min(natural[c], freeMin)-min(natural[c], freeLow))
		}
	}
	for deficit := gaps() + measured() + need - width; deficit > 0; deficit-- {
		pick := -1
		for _, wantID := range []bool{false, true} {
			if wantID && pick < 0 && squeeze > 0 {
				squeeze--
				pick = -2
				break
			}
			for _, c := range visible {
				if cols[c].free || cols[c].keep || cols[c].id != wantID || w[c] <= max(1, floor(c)) {
					continue
				}
				if pick < 0 || w[c] >= w[pick] {
					pick = c
				}
			}
			if pick != -1 {
				break
			}
		}
		if pick == -2 {
			continue
		}
		if pick < 0 {
			break
		}
		w[pick]--
	}

	act := active()
	got := share(width-gaps()-measured(), natural, act)
	for i, c := range act {
		w[c] = max(1, got[i])
	}

	// start is the cell each drawn column begins at within a line.
	start := make([]int, len(cols))
	at := 0
	for _, c := range visible {
		if stacked[c] {
			continue
		}
		if at > 0 {
			at += cols[c].gap
		}
		start[c] = at
		at += w[c]
	}

	out := make([]laidRow, len(rows))
	for ri, r := range rows {
		rowMax := maxRows
		isWhole := ri < len(whole) && whole[ri]
		if isWhole {
			rowMax = wholeRows
		}
		cell := func(c int) string {
			if c < len(r) {
				return r[c]
			}
			return ""
		}
		// A free cell with nothing after it on its row takes the room of the
		// columns after it as well: a clock only some rows have does not narrow
		// the words of the rows without one.
		room := func(c int) int {
			extra := 0
			for _, d := range visible {
				if d <= c || stacked[d] {
					continue
				}
				if cell(d) != "" {
					return 0
				}
				extra += cols[d].gap + w[d]
			}
			return extra
		}
		lines := map[int][]string{}
		// cuts are the row's cuts: the column whose line it is on, the line and
		// the whole value.
		type cutAt struct {
			col, line int
			value     string
		}
		var cuts []cutAt
		for _, c := range visible {
			switch {
			case stacked[c]:
			case cols[c].free:
				l, cut := wrapCellCut(cell(c), w[c]+room(c), rowMax)
				lines[c] = l
				if cut {
					cuts = append(cuts, cutAt{c, len(l) - 1, cell(c)})
				}
			case isWhole:
				lines[c] = wrapCell(cell(c), w[c], wholeRows)
			default:
				lines[c] = []string{cutCell(cell(c), w[c])}
				if width0(cell(c)) > w[c] {
					cuts = append(cuts, cutAt{c, 0, cell(c)})
				}
			}
		}
		if len(act) > 0 {
			head := act[0]
			for _, c := range visible {
				if stacked[c] {
					l, cut := wrapCellCut(cell(c), w[head], rowMax)
					if cut {
						cuts = append(cuts, cutAt{head, len(lines[head]) + len(l) - 1, cell(c)})
					}
					lines[head] = append(lines[head], l...)
				}
			}
		}
		n := 1
		for _, l := range lines {
			n = max(n, len(l))
		}
		out[ri] = laidRow{lines: make([]string, n), cuts: make([][]cutMark, n)}
		for _, k := range cuts {
			x := start[k.col] + width0(lines[k.col][k.line]) - 1
			out[ri].cuts[k.line] = append(out[ri].cuts[k.line], cutMark{x: x, value: ansi.Strip(k.value)})
		}
		for i := 0; i < n; i++ {
			var b strings.Builder
			first := true
			for _, c := range visible {
				if stacked[c] {
					continue
				}
				if !first {
					b.WriteString(spaces(cols[c].gap))
				}
				first = false
				text := ""
				if i < len(lines[c]) {
					text = lines[c][i]
				}
				b.WriteString(text)
				b.WriteString(spaces(w[c] - width0(text)))
			}
			out[ri].lines[i] = fit(strings.TrimRight(b.String(), " "), width)
		}
	}
	return out
}

// width0 is the cells of a cell, none for an empty one.
func width0(s string) int {
	if s == "" {
		return 0
	}
	return width(s)
}

// share divides room among the columns of cols the way water fills: a column
// whose cells are narrower than an even share takes what it needs and leaves
// the rest to the others. The result follows cols.
func share(room int, natural []int, cols []int) []int {
	out := make([]int, len(cols))
	order := make([]int, len(cols))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return natural[cols[order[a]]] < natural[cols[order[b]]] })
	room = max(room, 0)
	for k, i := range order {
		each := room / (len(order) - k)
		out[i] = min(natural[cols[i]], each)
		room -= out[i]
	}
	return out
}

// cutCell is a measured cell cut to w cells, ending in an ellipsis when it cut.
func cutCell(s string, w int) string {
	if width0(s) <= w {
		return s
	}
	return fit(s, w)
}

// wrapCell breaks s at words into rows of at most w cells: no more than
// maxRows of them, the last ending in an ellipsis when s goes on. A word wider
// than w is broken at w. Color open at a break is reopened on the next row.
func wrapCell(s string, w, maxRows int) []string {
	lines, _ := wrapCellCut(s, w, maxRows)
	return lines
}

// wrapCellCut is wrapCell that also says whether it cut s.
func wrapCellCut(s string, w, maxRows int) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	w = max(w, 1)
	if width(s) <= w {
		return []string{s}, false
	}
	// A hyphen is always a place Wrap may break at, which would split an id or a
	// branch name; a non-breaking one is not, and is put back after.
	lines := strings.Split(ansi.Wrap(strings.ReplaceAll(s, "-", nonBreaking), w, ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(strings.ReplaceAll(l, nonBreaking, "-"), " ")
	}
	cut := len(lines) > maxRows
	if cut {
		lines = lines[:maxRows]
		last := ansi.Truncate(lines[maxRows-1], w-1, "")
		lines[maxRows-1] = last + "…"
	}
	open := ""
	for i, l := range lines {
		l = open + l
		open = openStyle(l)
		if open != "" {
			l += "\x1b[0m"
		}
		lines[i] = l
	}
	return lines, cut
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// openStyle is the escape sequences still in force at the end of s.
func openStyle(s string) string {
	open := ""
	for _, seq := range sgr.FindAllString(s, -1) {
		if seq == "\x1b[0m" || seq == "\x1b[m" {
			open = ""
			continue
		}
		open += seq
	}
	return open
}

// laidWithin lays cells out under cols in width cells and takes at most room
// lines: free text wraps to as many rows as it needs, and a list short of room
// asks for fewer rows of each before it gives up a row.
func laidWithin(cols []column, cells [][]string, width, room int) [][]string {
	laid := laidWithinMarked(cols, cells, width, room, nil, false)
	out := make([][]string, len(laid))
	for i, r := range laid {
		out[i] = r.lines
	}
	return out
}

// laidWithinMarked is laidWithin with the rows whole says laid out whole.
func laidWithinMarked(cols []column, cells [][]string, width, room int, whole []bool, named bool) []laidRow {
	for rows := wrapRows; ; rows-- {
		// The room is spent on the rows as they are folded: an expanded row takes
		// more than its room and gives no other row up.
		laid := layoutMarked(cols, cells, width, rows, nil, named)
		used := 0
		for _, r := range laid {
			used += len(r.lines)
		}
		if used <= room || rows <= 1 {
			if whole != nil {
				laid = layoutMarked(cols, cells, width, rows, whole, named)
			}
			return laid
		}
	}
}
