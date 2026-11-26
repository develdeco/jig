package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// leftClick is a click of the left button on terminal cell x, y.
func leftClick(g *rig, x, y int) {
	g.t.Helper()
	g.send(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

// markCell is the terminal cell of the "…" that ends the row of id on the frame,
// and whether the row has one.
func markCell(frame, id string) (x, y int, ok bool) {
	lines := strings.Split(stripANSI(frame), "\n")
	in := false
	for i, l := range lines {
		switch {
		case strings.Contains(l, id+"  "):
			in = true
		case in && strings.Contains(l, "│          "):
			// a continuation row: the icon's and the id's columns are empty
		default:
			in = false
		}
		if at := strings.Index(l, "…"); in && at >= 0 {
			return len([]rune(l[:at])), i, true
		}
	}
	return 0, 0, false
}

// expandedIn is how many rows the screen on top has expanded.
func expandedIn(g *rig) int { return len(g.m.top().expanded) }

// TestAClickOnACutMarkExpandsItsRow: a click on the "…" of a cut row selects the
// row, gives its pane the focus and lays it out whole, as space does, on the
// project's list and on a chart's tickets.
func TestAClickOnACutMarkExpandsItsRow(t *testing.T) {
	t.Parallel()
	t.Run("main", func(t *testing.T) {
		t.Parallel()
		g := cutRig(t, 80, 40, 20).onRow("JIG-1")
		frame := g.view()
		x, y, ok := markCell(frame, "JIG-2")
		if !ok {
			t.Fatalf("JIG-2 has no mark:\n%s", frame)
		}
		leftClick(g, x, y)
		if r := g.m.refIn(g.m.page()); r == nil || r.id != "JIG-2" {
			t.Errorf("the click did not select the row it expanded: %+v", r)
		}
		block := blockOf(g.view(), "JIG-2")
		if strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(20)) {
			t.Errorf("the click did not lay the row out whole:\n%s", block)
		}
		if !strings.Contains(blockOf(g.view(), "JIG-1"), "…") {
			t.Errorf("another row changed")
		}
	})
	t.Run("chart", func(t *testing.T) {
		t.Parallel()
		g := sized(longChartRig(t, 20), 80, 40).openChart("checkout").onRow("JIG-5")
		g.tabTo("Detail")
		frame := g.view()
		x, y, ok := markCell(frame, "JIG-8")
		if !ok {
			t.Fatalf("JIG-8 has no mark:\n%s", frame)
		}
		leftClick(g, x, y)
		if got := g.m.focusedPane(); got != "Tickets" {
			t.Errorf("the click left the focus on %q", got)
		}
		if r := g.m.refIn(g.m.page()); r == nil || r.id != "JIG-8" {
			t.Errorf("the click did not select the row it expanded: %+v", r)
		}
		block := blockOf(g.view(), "JIG-8")
		if strings.Contains(block, "…") || !hasWordsInOrder(block, cutTitle(20)) {
			t.Errorf("the click did not lay the row out whole:\n%s", block)
		}
	})
}

// TestAClickBesideAMarkOnlySelectsItsRow: the cell before a mark counts as the
// mark (a pointer a cell short of a one-cell "…" reaches it); any other cell of
// the row, or a cell after the mark, only selects the row. A click on an
// expanded row folds nothing.
func TestAClickBesideAMarkOnlySelectsItsRow(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 80, 40, 20).onRow("JIG-1")
	frame := g.view()
	x, y, ok := markCell(frame, "JIG-2")
	if !ok {
		t.Fatalf("JIG-2 has no mark:\n%s", frame)
	}
	for _, at := range []int{x + 1, x - 2, x - 10, 14} {
		leftClick(g, at, y)
		if expandedIn(g) != 0 {
			t.Fatalf("a click at cell %d (the mark is at %d) expanded the row", at, x)
		}
		if r := g.m.refIn(g.m.page()); r == nil || r.id != "JIG-2" {
			t.Errorf("a click at cell %d did not select the row: %+v", at, r)
		}
	}
	leftClick(g, x-1, y)
	if expandedIn(g) != 1 {
		t.Fatalf("a click on the cell before the mark did not expand the row")
	}
	leftClick(g, 14, y)
	if expandedIn(g) != 1 {
		t.Errorf("a click on an expanded row folded it")
	}
}

// TestAClickOnAWordsEllipsisOnlySelects: a "…" that is part of a word is no
// mark: a click on it selects its row and expands nothing.
func TestAClickOnAWordsEllipsisOnlySelects(t *testing.T) {
	t.Parallel()
	g := cutRig(t, 80, 40, 20)
	rows := cutRoster(1)
	rows[0].Title = "reading… the store"
	rows[1].Title = "second ticket"
	g.m.w.setRoster(rows)
	g.m.w.gen++
	g.onRow("JIG-2")
	frame := g.view()
	lines := strings.Split(stripANSI(frame), "\n")
	for y, l := range lines {
		if at := strings.Index(l, "JIG-1"); at >= 0 {
			dots := strings.Index(l, "…")
			if dots < 0 {
				t.Fatalf("the word's ellipsis is not drawn:\n%s", frame)
			}
			leftClick(g, len([]rune(l[:dots])), y)
			break
		}
	}
	if expandedIn(g) != 0 {
		t.Errorf("a click on a word's own ellipsis expanded a row")
	}
	if r := g.m.refIn(g.m.page()); r == nil || r.id != "JIG-1" {
		t.Errorf("the click did not select the row: %+v", r)
	}
}

// TestMarkCellsFollowTheWindowTheFrameAndAResize: a mark is clicked where the
// frame draws it: after the window has moved down a long list, after the
// terminal is resized, and with a pane zoomed.
func TestMarkCellsFollowTheWindowTheFrameAndAResize(t *testing.T) {
	t.Parallel()
	// A list longer than the window, the cut row at its end.
	g := cutRig(t, 80, 14, 50)
	rows := cutRoster(50)
	for i := 0; i < 8; i++ {
		rows = append([]rosterRow{{ID: "JIG-1" + string(rune('A'+i)), Title: "A short title", Digest: rows[0].Digest}}, rows...)
	}
	g.m.w.setRoster(rows)
	g.m.w.gen++
	g.onRow("JIG-2").press("down")
	frame := g.view()
	x, y, ok := markCell(frame, "JIG-2")
	if !ok || y < 3 {
		t.Fatalf("the window does not draw JIG-2's mark below the top:\n%s", frame)
	}
	leftClick(g, x, y)
	if expandedIn(g) != 1 || strings.Contains(blockOf(g.view(), "JIG-2"), "…") {
		t.Errorf("a click on a mark in a moved window did not expand its row:\n%s", g.view())
	}
	g.press("space")
	if expandedIn(g) != 0 {
		t.Fatalf("space did not fold the row")
	}

	// A resize moves the mark: the next click reads the cell drawn now.
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	x, y, ok = markCell(g.view(), "JIG-2")
	if !ok {
		t.Fatalf("no mark after the resize:\n%s", g.view())
	}
	leftClick(g, x, y)
	if expandedIn(g) != 1 {
		t.Errorf("a click on the mark after a resize did not expand its row")
	}

	// A zoomed pane's mark is where the zoomed frame draws it.
	c := sized(longChartRig(t, 20), 80, 40).openChart("checkout").onRow("JIG-8")
	c.press("z")
	x, y, ok = markCell(c.view(), "JIG-8")
	if !ok {
		t.Fatalf("no mark in the zoomed pane:\n%s", c.view())
	}
	leftClick(c, x, y)
	if expandedIn(c) != 1 || strings.Contains(blockOf(c.view(), "JIG-8"), "…") {
		t.Errorf("a click on a mark in a zoomed pane did not expand its row:\n%s", c.view())
	}
}
