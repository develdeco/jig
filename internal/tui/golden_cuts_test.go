package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cutGoldens are the golden frames that hold a cut: they are written through
// goldenCuts, which checks that every ellipsis of the frame is a registered mark
// and that each mark's holder lays out whole and folds back.
var cutGoldens = map[string]bool{
	"panel-ok":         true,
	"publish":          true,
	"review":           true,
	"review-published": true,
	"ticket-card":      true,
	"ticket-substeps":  true,
}

// ownWords are the TUI's own words that end in an ellipsis: not cuts.
var ownWords = []string{"reading the store…", "reading…"}

// cutCells are the cells of the frame that hold an ellipsis that is no word of
// the TUI's own, as (x, y).
func cutCells(frame string) [][2]int {
	var out [][2]int
	for y, line := range strings.Split(stripANSI(frame), "\n") {
		for _, w := range ownWords {
			line = strings.ReplaceAll(line, w, strings.Repeat(" ", width(w)))
		}
		x := 0
		for _, r := range line {
			if r == '…' {
				out = append(out, [2]int{x, y})
			}
			x++
		}
	}
	return out
}

// registeredMark is a cut mark the last frame drew, where and with its value.
type registeredMark struct {
	x, y  int
	value string
	last  bool
}

// marksDrawn are the marks the last frame of g drew: those of the body by the
// lines the window drew, and the last command's.
func marksDrawn(g *rig) []registeredMark {
	var out []registeredMark
	if h := g.m.hit; h != nil {
		for y := h.top; y < h.top+h.rows; y++ {
			line := h.start + y - h.top
			if line < 0 || line >= len(h.cuts) {
				continue
			}
			for _, c := range h.cuts[line] {
				out = append(out, registeredMark{x: c.x + frameLead, y: y, value: c.value})
			}
		}
	}
	if k := g.m.lastMark; k != nil {
		out = append(out, registeredMark{x: k.x, y: k.y, value: g.m.lastShownValue(), last: true})
	}
	return out
}

// lastShownValue is what the last command's mark stands for.
func (m *Model) lastShownValue() string {
	v, _ := m.lastShown(m.width)
	return v.value
}

// compact is s without whitespace and the characters of a box.
func compact(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '│', '╭', '╮', '╰', '╯', '─':
			return -1
		}
		return r
	}, s)
}

// charsInOrder says whether the characters of want are in text in order, whitespace
// and box edges apart.
func charsInOrder(text, want string) bool {
	have, need := []rune(compact(text)), []rune(compact(want))
	i := 0
	for _, r := range have {
		if i < len(need) && r == need[i] {
			i++
		}
	}
	return i == len(need)
}

// goldenCuts is golden for a frame that holds cuts: the frame is compared as
// golden does; every ellipsis in it must be a mark the screen registered; and
// each mark is expanded (by a click on it) to show the whole value it stands
// for, then folded to the frame it was.
func goldenCuts(t *testing.T, name string, g *rig) {
	t.Helper()
	frame := g.view()
	goldenFile(t, name, frame)
	if *update {
		return
	}
	marks := marksDrawn(g)
	for _, c := range cutCells(frame) {
		found := false
		for _, m := range marks {
			if m.x == c[0] && m.y == c[1] {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the ellipsis at cell %d,%d is no registered cut:\n%s", name, c[0], c[1], frame)
		}
	}
	for i := range marks {
		now := marksDrawn(g)
		if i >= len(now) {
			break
		}
		m := now[i]
		if !m.last {
			// A click on the row's first cell selects it, as a click on the mark
			// would, so what folding gives back is compared with that frame.
			leftClick(g, frameLead+1, m.y)
		}
		before := stripANSI(g.view())
		now = marksDrawn(g)
		if i >= len(now) {
			t.Errorf("%s: a mark went when its row was selected", name)
			break
		}
		m = now[i]
		leftClick(g, m.x, m.y)
		open := stripANSI(g.view())
		if len(g.m.top().expanded) == 0 && g.m.lastOpen == nil {
			t.Errorf("%s: a click on the cut at %d,%d expanded nothing:\n%s", name, m.x, m.y, before)
			continue
		}
		if !charsInOrder(open, m.value) {
			t.Errorf("%s: the expanded frame does not hold the whole value %q:\n%s", name, m.value, open)
		}
		if m.last {
			g.m.toggleLast(true)
		} else {
			g.press("space")
		}
		if folded := stripANSI(g.view()); folded != before {
			t.Errorf("%s: folding does not give the frame back:\n--- before\n%s\n--- after\n%s", name, before, folded)
		}
	}
}

// TestNoGoldenHoldsAnUnregisteredCut: a golden frame with an ellipsis that is
// not one of the TUI's own words is listed in cutGoldens, so it is written
// through goldenCuts and its marks are read; the list names no golden that does
// not exist.
func TestNoGoldenHoldsAnUnregisteredCut(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "*.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".golden")
		seen[name] = true
		if len(cutCells(string(data))) > 0 && !cutGoldens[name] {
			t.Errorf("%s holds a cut that is not registered: write it through goldenCuts and list it in cutGoldens", f)
		}
	}
	for name := range cutGoldens {
		if !seen[name] {
			t.Errorf("cutGoldens names %s, which has no golden file", name)
		}
	}
}
