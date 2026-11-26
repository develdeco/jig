package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The cut rows the expansion tests draw: a title of 130 numbered words, which
// the allocator cuts at every size the screens meet.

// cutTitle is a title of n numbered words.
func cutTitle(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("w%03d", i+1)
	}
	return strings.Join(words, " ")
}

// cutRoster is a project of two tickets with the long title and one with a short.
func cutRoster(n int) []rosterRow {
	d := task("build", true, "done|approved", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish")
	return []rosterRow{
		{ID: "JIG-1", Title: cutTitle(n), Digest: d},
		{ID: "JIG-2", Title: cutTitle(n), Digest: d},
		{ID: "JIG-3", Title: "A short title", Digest: d},
	}
}

// cutRig is the project's list of cutRoster at w by h.
func cutRig(t *testing.T, w, h, n int) *rig {
	t.Helper()
	g := newRig(t, "")
	g.m.w.setRoster(cutRoster(n))
	g.m.w.gen++
	return sized(g, w, h)
}

// blockOf is the lines of the row of id on the frame: its first line and the
// continuation rows under it.
func blockOf(frame, id string) string {
	return strings.Join(rowBlock(strings.Split(stripANSI(frame), "\n"), id), "\n")
}

// hasWordsInOrder says whether every word of title is in text, in order.
func hasWordsInOrder(text, title string) bool {
	pos := 0
	for _, word := range strings.Fields(title) {
		i := strings.Index(text[pos:], word)
		if i < 0 {
			return false
		}
		pos += i + len(word)
	}
	return true
}

var allocatorColumns = []column{{}, {id: true, gap: 1}, {free: true, gap: 2}, {gap: 2}, {free: true, gap: 2}, {gap: 2, keep: true}}

// TestAllocatorWholeRowWrapsCutCellsUnderTheirColumns: a row laid out whole
// draws every word of its title, with no "…", on rows under the title's column.
func TestAllocatorWholeRowWrapsCutCellsUnderTheirColumns(t *testing.T) {
	t.Parallel()
	title := cutTitle(60)
	rows := [][]string{{"o", "JIG-1", title, "...", "ready", "3m"}, {"o", "JIG-2", "short", "...", "ready", "4m"}}
	folded := layoutMarked(allocatorColumns, rows, 60, wrapRows, nil, false)
	whole := layoutMarked(allocatorColumns, rows, 60, wrapRows, []bool{true, false}, false)
	if len(folded[0].lines) != wrapRows || !strings.HasSuffix(folded[0].lines[wrapRows-1], "…") {
		t.Fatalf("the folded row is not cut after %d rows:\n%s", wrapRows, strings.Join(folded[0].lines, "\n"))
	}
	got := strings.Join(whole[0].lines, "\n")
	if strings.Contains(got, "…") || !hasWordsInOrder(got, title) || len(whole[0].lines) <= wrapRows {
		t.Errorf("the whole row is not the whole title on more rows:\n%s", got)
	}
	// The continuation rows start under the title's column: after the icon, the
	// id and the gaps, which is where the first row's title starts.
	at := strings.Index(whole[0].lines[0], "w001")
	for i, l := range whole[0].lines[1:] {
		if strings.TrimLeft(l, " ") == "" || len(l)-len(strings.TrimLeft(l, " ")) != at {
			t.Errorf("continuation row %d does not start under the title (column %d): %q", i+1, at, l)
		}
	}
	if strings.Join(whole[1].lines, "\n") != strings.Join(folded[1].lines, "\n") {
		t.Errorf("a row that was not expanded moved:\n%v\n%v", whole[1].lines, folded[1].lines)
	}
}

// TestAllocatorWholeRowMovesNoOtherColumn: expanding a row keeps the width every
// column had, so the cells of its first line are where the folded row had them.
func TestAllocatorWholeRowMovesNoOtherColumn(t *testing.T) {
	t.Parallel()
	rows := [][]string{{"o", "JIG-1", cutTitle(60), "...", "ready", "3m"}, {"o", "JIG-22", "short", "...", "waiting on the review", "4m"}}
	folded := layoutMarked(allocatorColumns, rows, 70, wrapRows, nil, false)
	whole := layoutMarked(allocatorColumns, rows, 70, wrapRows, []bool{true, false}, false)
	if folded[0].lines[0] != whole[0].lines[0] {
		t.Errorf("the first line of the row moved:\n%q\n%q", folded[0].lines[0], whole[0].lines[0])
	}
	for i := range folded[1].lines {
		if folded[1].lines[i] != whole[1].lines[i] {
			t.Errorf("another row moved: %q became %q", folded[1].lines[i], whole[1].lines[i])
		}
	}
}

// TestACutRegistersAMarkWithItsWholeValue: a cell the allocator cuts leaves a
// mark on the line its "…" is drawn on, at the cell it sits on, with the whole
// value; the same row laid out whole registers none.
func TestACutRegistersAMarkWithItsWholeValue(t *testing.T) {
	t.Parallel()
	title := cutTitle(60)
	rows := [][]string{{"o", "JIG-1", title, "...", "ready", "3m"}}
	folded := layoutMarked(allocatorColumns, rows, 60, wrapRows, nil, false)[0]
	if !folded.cut() {
		t.Fatal("a cut row registers no mark")
	}
	n := 0
	for i, cuts := range folded.cuts {
		for _, c := range cuts {
			n++
			if i != len(folded.lines)-1 {
				t.Errorf("the mark is on line %d, the cut is on line %d", i, len(folded.lines)-1)
			}
			if cell := []rune(stripANSI(folded.lines[i])); c.x >= len(cell) || cell[c.x] != '…' {
				t.Errorf("the mark's cell %d of %q is not the ellipsis", c.x, folded.lines[i])
			}
			if c.value != title {
				t.Errorf("the mark's value is %q, want the whole title", c.value)
			}
		}
	}
	if n != 1 {
		t.Errorf("a row with one cut cell registers %d marks", n)
	}
	// A cut id is a measured cell: it registers a mark too.
	id := layoutMarked(allocatorColumns, [][]string{{"o", "JIG-0000000000000000000000001", "t", "...", "ready", "3m"}}, 40, wrapRows, nil, false)[0]
	if !id.cut() {
		t.Errorf("a cut id registers no mark:\n%s", strings.Join(id.lines, "\n"))
	}
	if whole := layoutMarked(allocatorColumns, rows, 60, wrapRows, []bool{true}, false)[0]; whole.cut() {
		t.Errorf("a row laid out whole registers a mark")
	}
}

// TestAWordWithItsOwnEllipsisRegistersNoMark: an ellipsis that is part of a word
// ("reading…") is no cut, so it registers no mark and is never a click target.
func TestAWordWithItsOwnEllipsisRegistersNoMark(t *testing.T) {
	t.Parallel()
	rows := [][]string{{"o", "JIG-1", "reading… the store…", "...", "reading…", "3m"}}
	laid := layoutMarked(allocatorColumns, rows, 80, wrapRows, nil, false)[0]
	if laid.cut() || !strings.Contains(laid.lines[0], "reading…") {
		t.Errorf("a word with its own ellipsis registered a mark or was lost: %v %v", laid.cuts, laid.lines)
	}
	g := cutRig(t, 80, 24, 130)
	g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "reading… the store", Digest: cutRoster(1)[0].Digest}})
	g.m.w.gen++
	g.view()
	for _, l := range g.m.page().lines {
		if len(l.cuts) > 0 {
			t.Errorf("a screen registered a mark for a word's own ellipsis: %q", l.text)
		}
	}
	if g.m.keysOn.cut {
		t.Errorf("a row with nothing cut is said to hold a cut")
	}
}

// TestSpaceExpandsTheRowUnderTheCursorAndFoldsItBack: space lays the cut row
// under the cursor out whole where it stands, moving the rows below and no
// other cell, and space again draws exactly the frame it had before.
func TestSpaceExpandsTheRowUnderTheCursorAndFoldsItBack(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 80, 40, 20)
	g.onRow("JIG-1")
	before := stripANSI(g.view())
	if !strings.Contains(blockOf(before, "JIG-1"), "…") {
		t.Fatalf("the row is not cut:\n%s", before)
	}
	if hints := strings.Join(strings.Fields(before), " "); !strings.Contains(hints, "space expand") {
		t.Errorf("the hint line does not offer space to expand:\n%s", before)
	}
	g.press("space")
	open := stripANSI(g.view())
	block := blockOf(open, "JIG-1")
	if strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(20)) {
		t.Errorf("space did not lay the title out whole:\n%s", open)
	}
	if !strings.Contains(strings.Join(strings.Fields(open), " "), "space fold") {
		t.Errorf("the hint line does not offer space to fold:\n%s", open)
	}
	g.press("space")
	if folded := stripANSI(g.view()); folded != before {
		t.Errorf("folding does not draw the frame it had before:\n--- before\n%s\n--- after\n%s", before, folded)
	}
}

// TestSpaceWithNothingCutSaysSo: on a row with nothing cut space does not run,
// the hint line does not list it and the notice says why.
func TestSpaceWithNothingCutSaysSo(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 80, 40, 20)
	g.onRow("JIG-3")
	view := stripANSI(g.view())
	if strings.Contains(strings.Join(strings.Fields(view), " "), "space ") {
		t.Errorf("the hint line lists space on a row with nothing cut:\n%s", view)
	}
	g.press("space")
	if g.m.notice != "nothing is cut here" {
		t.Errorf("the notice is %q, want %q", g.m.notice, "nothing is cut here")
	}
	if got := stripANSI(g.view()); !strings.Contains(got, "nothing is cut here") || len(g.m.nav[0].expanded) != 0 {
		t.Errorf("space expanded something or did not say so:\n%s", got)
	}
}

// TestAnExpandedRowStaysWhenALineArrivesAboveIt: an expansion is the row's, by
// its identity, so a row arriving above it and a resize leave it expanded.
func TestAnExpandedRowStaysWhenALineArrivesAboveIt(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 80, 60, 20)
	g.onRow("JIG-2").press("space")
	if !strings.Contains(g.view(), "w020") {
		t.Fatal("space did not expand the row")
	}
	rows := append([]rosterRow{{ID: "JIG-0", Title: "Arrived above", Digest: cutRoster(1)[0].Digest}}, cutRoster(20)...)
	g.m.w.setRoster(rows)
	g.m.w.gen++
	block := blockOf(g.view(), "JIG-2")
	if strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(20)) {
		t.Errorf("the row folded when a line arrived above it:\n%s", block)
	}
	g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
	block = blockOf(g.view(), "JIG-2")
	if strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(20)) {
		t.Errorf("the row folded when the terminal was resized:\n%s", block)
	}
}

// TestAnExpansionEndsWithItsScreen: a chart's expanded row is folded when the
// screen is closed and opened again.
func TestAnExpansionEndsWithItsScreen(t *testing.T) {
	t.Parallel()
	g := sized(longChartRig(t, 20), 80, 50).openChart("checkout")
	g.onRow("JIG-8")
	if !strings.Contains(blockOf(g.view(), "JIG-8"), "…") {
		t.Fatalf("the chart's row is not cut:\n%s", g.view())
	}
	g.press("space")
	if block := blockOf(g.view(), "JIG-8"); strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(20)) {
		t.Fatalf("space did not expand the chart's row:\n%s", block)
	}
	g.press("esc").openChart("checkout")
	if block := blockOf(g.view(), "JIG-8"); !strings.Contains(block, "…") {
		t.Errorf("the screen opened again is not folded:\n%s", block)
	}
}

// longChartRig is the chart fixtures with a long title on JIG-8.
func longChartRig(t *testing.T, n int) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	title := func(v map[string]any) {
		for _, e := range v["tickets"].([]map[string]any) {
			if e["id"] == "JIG-8" {
				e["title"] = cutTitle(n)
			}
		}
	}
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(title), status)
	return g.feed(lines...)
}

// TestMainAndChartRowsExpandAtThreeSizes: at a narrow, a standard and a wide
// terminal a cut row of main, and of a chart's tickets, expands to its whole
// title, no line is wider than the terminal, and folding gives the frame back.
func TestMainAndChartRowsExpandAtThreeSizes(t *testing.T) {
	t.Parallel()
	// n is the words of the title: more than three rows of the title's column at
	// the size, and not so many that the row outgrows the pane (a row taller
	// than its pane is read by lines, which is its own test).
	for _, size := range [][3]int{{60, 20, 20}, {80, 24, 20}, {230, 50, 160}} {
		w, h, n := size[0], size[1], size[2]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			for _, screen := range []string{"main", "chart"} {
				var g *rig
				id := "JIG-1"
				if screen == "main" {
					g = cutRig(t, w, h, n).onRow(id)
				} else {
					id = "JIG-8"
					g = sized(longChartRig(t, n), w, h).openChart("checkout").onRow(id)
				}
				g.view()
				before := stripANSI(g.view())
				if !strings.Contains(blockOf(before, id), "…") {
					t.Fatalf("%s: the row is not cut at %dx%d:\n%s", screen, w, h, before)
				}
				g.press("space")
				open := stripANSI(g.view())
				for _, l := range strings.Split(open, "\n") {
					if width(l) > w {
						t.Errorf("%s: a line is %d cells in a %d wide terminal: %q", screen, width(l), w, l)
					}
				}
				if block := blockOf(open, id); strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(n)) {
					t.Errorf("%s: the row is not whole at %dx%d:\n%s", screen, w, h, open)
				}
				g.press("space")
				if folded := stripANSI(g.view()); folded != before {
					t.Errorf("%s: folding does not give the frame back at %dx%d:\n--- before\n%s\n--- after\n%s", screen, w, h, before, folded)
				}
			}
		})
	}
}
