package tui

import (
	"os"
	"path/filepath"
	"slices"
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
	lines := strings.Split(stripANSI(frame), "\n")
	// The header and the keys are the screen's chrome, which is not a pane.
	footer := len(lines)
	for i, l := range lines {
		if i > 1 && strings.HasPrefix(l, "───") {
			footer = i
		}
	}
	for y, line := range lines {
		if y < 2 || y >= footer {
			continue
		}
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

// markOnCursor says whether the mark is on a line of the row the cursor stands on.
func (m *Model) markOnCursor(k registeredMark) bool {
	h := m.hit
	if h == nil || k.last {
		return false
	}
	line := h.start + k.y - h.top
	sel, ok := h.rowOf[h.holder[min(max(line, 0), len(h.holder)-1)]]
	return line >= 0 && line < len(h.holder) && h.held[line] && ok && sel == m.top().sel
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
	goldenFile(t, name, g.view())
	if *update {
		return
	}
	everyCutHasAHolder(t, name, g)
}

// everyCutHasAHolder checks the frame g draws: every ellipsis in it that is no
// word of the TUI's own is a registered mark, a click on a mark expands its
// holder to a frame that holds the whole value it stands for, and folding gives
// the frame back. It moves the cursor, so it takes a rig of its own.
func everyCutHasAHolder(t *testing.T, name string, g *rig) {
	t.Helper()
	frame := g.view()
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
			// A click on the mark selects its row, which moves the window about it:
			// the row is selected first, and what folding gives back is compared
			// with the frame the selection left.
			leftClick(g, m.x, m.y)
			g.press("space")
			if len(g.m.top().expanded) > 0 {
				g.press("space")
			}
			g.view()
		}
		before := stripANSI(g.view())
		// The window moved with the selection: the mark is found again by what it
		// stands for.
		again := marksDrawn(g)
		same := func(o registeredMark) bool { return o.value == m.value && o.last == m.last }
		at := slices.IndexFunc(again, func(o registeredMark) bool { return same(o) && g.m.markOnCursor(o) })
		if at < 0 {
			at = slices.IndexFunc(again, same)
		}
		if at < 0 {
			t.Errorf("%s: the mark of %q went when its row was selected", name, m.value)
			continue
		}
		m = again[at]
		leftClick(g, m.x, m.y)
		open := stripANSI(g.view())
		if len(g.m.top().expanded) == 0 && g.m.lastOpen == nil {
			t.Errorf("%s: a click on the cut at %d,%d expanded nothing:\n%s", name, m.x, m.y, before)
			continue
		}
		// A row taller than its pane is read by lines: the value is in the frames
		// read down to its end.
		seen := open
		for i := 0; i < 80 && !charsInOrder(seen, m.value) && g.m.tall != nil && g.m.top().rowScroll < g.m.tall.maxScroll(); i++ {
			g.press("down")
			seen += "\n" + stripANSI(g.view())
		}
		// A pane of text is read by lines too, and put back where it was.
		scrolled := 0
		for g.m.top().textFocus && scrolled < 80 && !charsInOrder(seen, m.value) {
			g.press("down")
			scrolled++
			seen += "\n" + stripANSI(g.view())
		}
		for ; scrolled > 0; scrolled-- {
			g.press("up")
		}
		if !charsInOrder(seen, m.value) {
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
