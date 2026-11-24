package tui

import (
	"strings"
	"testing"
)

// litCells walks the escape codes of one drawn line and returns each visible
// cell with whether the lit background is on when it is drawn.
func litCells(line string) (cells []string, lit []bool) {
	on := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && (line[j] < '@' || line[j] > '~') {
				j++
			}
			if j < len(line) && line[j] == 'm' {
				params := line[i+2 : j]
				switch {
				case params == "" || params == "0":
					on = false
				case strings.Contains(params, "48;5;"+litBackground):
					on = true
				}
			}
			i = j + 1
			continue
		}
		r := []rune(line[i:])[0]
		cells = append(cells, string(r))
		lit = append(lit, on)
		i += len(string(r))
	}
	return cells, lit
}

// undecorated is a colored screen as its text: the escape codes gone, and the
// spaces a lit row is padded with after its text, which a plain screen trims.
func undecorated(s string) string {
	lines := strings.Split(stripANSI(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

// coloredPage is a model drawing with the colors of a terminal.
func coloredPage(t *testing.T) *Model {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.m.st = styles{r: colored()}
	return g.m
}

// TestSelectedRowIsLitAcrossItsWidth: the row the cursor stands on is lit from
// the left border to the right border of its panel, and across the screen for
// a loose row, though its cells carry colors of their own that reset the lit
// background.
func TestSelectedRowIsLitAcrossItsWidth(t *testing.T) {
	t.Parallel()
	m := coloredPage(t)
	cell := m.st.state(StateGreen, "green") + " then " + m.st.dim("dim") + " and more"
	var pg page
	pg.boxTop("Slices", "2")
	pg.boxRow(rowRef{kind: rowSlice, id: "a"}, cell)
	pg.boxRow(rowRef{kind: rowSlice, id: "b"}, cell)
	pg.boxEnd()
	pg.addRow(rowRef{kind: rowSlice, id: "c"}, cell)
	const w = 60

	for name, c := range map[string]struct {
		sel, line, other int
		first, last      int // the cells that must be lit: first up to last
	}{
		"a panel row": {0, 1, 2, 3, w - 1},
		"a loose row": {2, 4, 1, 0, w},
	} {
		out := m.window(pg, c.sel, 0, len(pg.lines), w)
		cells, lit := litCells(out[c.line])
		if got := len(cells); got != w {
			t.Fatalf("%s: the line is %d cells, want %d: %q", name, got, w, out[c.line])
		}
		for i := range cells {
			if want := i >= c.first && i < c.last; lit[i] != want {
				t.Errorf("%s: cell %d %q lit=%v, want %v: %q", name, i, cells[i], lit[i], want, out[c.line])
			}
		}
		// A row the cursor is not on is not lit anywhere.
		_, lit = litCells(out[c.other])
		for i, on := range lit {
			if on {
				t.Errorf("%s: the row off the cursor is lit at cell %d", name, i)
			}
		}
	}
}

// TestPlainModeKeepsTheSelectionMarkerAlone: a renderer with no color profile
// draws the marker and nothing else, so a frame read on a pipe is unchanged.
func TestPlainModeKeepsTheSelectionMarkerAlone(t *testing.T) {
	t.Parallel()
	m := newRig(t, "JIG-1").m
	var pg page
	pg.boxTop("Slices", "")
	pg.boxRow(rowRef{kind: rowSlice, id: "a"}, "alpha")
	pg.boxEnd()
	pg.addRow(rowRef{kind: rowSlice, id: "b"}, "beta")
	const w = 40
	out := m.window(pg, 0, 0, len(pg.lines), w)
	if !strings.Contains(out[1], "│▸ alpha") || strings.Contains(strings.Join(out, ""), "\x1b") {
		t.Errorf("the plain panel row is %q", out[1])
	}
	out = m.window(pg, 1, 0, len(pg.lines), w)
	if out[3] != "▸ beta" {
		t.Errorf("the plain loose row is %q, want the marker and its text only", out[3])
	}
}

// TestAFocusedPanelDrawsItsBorderLit: a panel whose lines say they are focused
// draws its edges in the key color, as a key in the hint line is drawn, and one
// that is not draws them dim.
func TestAFocusedPanelDrawsItsBorderLit(t *testing.T) {
	t.Parallel()
	m := coloredPage(t)
	build := func(focus bool) page {
		var pg page
		pg.boxTop("Slices", "1")
		pg.boxRow(rowRef{kind: rowSlice, id: "a"}, "alpha")
		pg.boxEnd()
		for i := range pg.lines {
			pg.lines[i].focus = focus
		}
		return pg
	}
	const w = 40
	keyOn, _, _ := strings.Cut(m.st.key("╭"), "╭")
	dimOn, _, _ := strings.Cut(m.st.dim("╭"), "╭")
	for name, focus := range map[string]bool{"focused": true, "unfocused": false} {
		pg := build(focus)
		out := m.window(pg, 0, 0, len(pg.lines), w)
		for i, edge := range []string{"╭", "│", "╰"} {
			line := out[i]
			at := strings.Index(line, edge)
			if at < 0 {
				t.Fatalf("%s: no %q in %q", name, edge, line)
			}
			head := line[:at]
			got := head[strings.LastIndex(head, "\x1b["):]
			want := dimOn
			if focus {
				want = keyOn
			}
			if got != want {
				t.Errorf("%s: the edge %q is drawn with %q, want %q", name, edge, got, want)
			}
		}
	}
}
