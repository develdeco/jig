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

// layout draws rows of cells under cols in width cells. Each row comes back as
// the lines it takes: its first line, and the continuation rows of a free cell
// that wrapped, aligned under their column. A column no row has a cell for is
// not drawn. Cells may carry color; it is kept on every line a cell wraps to.
//
// Measured columns are as wide as their widest cell. They are cut only when
// they and freeMin cells for the free text leave no room: then the widest one
// that is not an id gives way first, each ending in an ellipsis, and ids last.
//
// Free columns share the rest. When a free column after the first would get
// fewer than freeMin cells it needs, the free columns after the first stack
// under the first's left edge instead of sitting beside it.
func layout(cols []column, rows [][]string, width int) [][]string {
	return layoutRows(cols, rows, width, wrapRows)
}

// layoutRows is layout with free cells held to at most maxRows rows each: a list
// short of room asks for fewer than wrapRows before it drops a row.
func layoutRows(cols []column, rows [][]string, width, maxRows int) [][]string {
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
		for i := 1; i < len(free); i++ {
			if got[i] < min(natural[free[i]], freeMin) {
				for _, o := range free[1:] {
					stacked[o] = true
				}
				break
			}
		}
	}

	// Measured columns give way only when 20 cells for the free text do not
	// fit beside them.
	need := 0
	for _, c := range active() {
		need += min(natural[c], freeMin)
	}
	for deficit := gaps() + measured() + need - width; deficit > 0; deficit-- {
		pick := -1
		for _, wantID := range []bool{false, true} {
			for _, c := range visible {
				if cols[c].free || cols[c].keep || cols[c].id != wantID || w[c] <= 1 {
					continue
				}
				if pick < 0 || w[c] >= w[pick] {
					pick = c
				}
			}
			if pick >= 0 {
				break
			}
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

	out := make([][]string, len(rows))
	for ri, r := range rows {
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
		for _, c := range visible {
			switch {
			case stacked[c]:
			case cols[c].free:
				lines[c] = wrapCell(cell(c), w[c]+room(c), maxRows)
			default:
				lines[c] = []string{cutCell(cell(c), w[c])}
			}
		}
		if len(act) > 0 {
			head := act[0]
			for _, c := range visible {
				if stacked[c] {
					lines[head] = append(lines[head], wrapCell(cell(c), w[head], maxRows)...)
				}
			}
		}
		n := 1
		for _, l := range lines {
			n = max(n, len(l))
		}
		out[ri] = make([]string, n)
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
			out[ri][i] = fit(strings.TrimRight(b.String(), " "), width)
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
	if s == "" {
		return nil
	}
	w = max(w, 1)
	if width(s) <= w {
		return []string{s}
	}
	// A hyphen is always a place Wrap may break at, which would split an id or a
	// branch name; a non-breaking one is not, and is put back after.
	lines := strings.Split(ansi.Wrap(strings.ReplaceAll(s, "-", nonBreaking), w, ""), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(strings.ReplaceAll(l, nonBreaking, "-"), " ")
	}
	if len(lines) > maxRows {
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
	return lines
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
