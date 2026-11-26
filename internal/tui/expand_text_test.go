package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// detailRig is the chart screen with a held agent on a screen narrow enough that
// the Detail panel's hold row is cut, the Detail pane focused: it has no cursor
// rows.
func detailRig(t *testing.T, w, h int) *rig {
	t.Helper()
	g := heldChartRig(t)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	onChart(g)
	g.tabTo("Detail")
	return g
}

// holdWords is what the hold row of the Detail panel says, whole.
const holdWords = "⏸ agent 4f2a91c0 held at its stop · commit 222222222222 for slice s2 has no recorded result"

// panelText is the words of the panel titled title on the frame.
func panelText(t *testing.T, frame, title string) string {
	t.Helper()
	panel := panelLines(strings.Split(stripANSI(frame), "\n"), title)
	if panel == nil {
		t.Fatalf("the screen has no %s panel:\n%s", title, frame)
	}
	return strings.Join(strings.Fields(strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(strings.Join(panel, "\n"))), " ")
}

// TestSpaceExpandsTheCutLinesOfAPaneWithNoRows: space on a focused pane that has
// no cursor rows lays every cut line of it out whole, and says fold; space again
// folds it back.
func TestSpaceExpandsTheCutLinesOfAPaneWithNoRows(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{40, 50}, {80, 24}, {100, 30}} {
		g := detailRig(t, size[0], size[1])
		before := stripANSI(g.view())
		detail := panelText(t, before, "Detail")
		cut := strings.Contains(detail, "…")
		if size[0] == 40 && !cut {
			t.Fatalf("the hold row is not cut at 40 wide:\n%s", before)
		}
		if !cut {
			// A screen that holds the row whole has nothing to expand there.
			g.press("space")
			if g.m.notice != "nothing is cut here" {
				t.Errorf("%dx%d: nothing is cut and the notice is %q", size[0], size[1], g.m.notice)
			}
			continue
		}
		if hint := strings.Join(strings.Fields(before), " "); !strings.Contains(hint, "space expand") {
			t.Errorf("the hint line does not offer space to expand:\n%s", before)
		}
		g.press("space")
		open := stripANSI(g.view())
		if got := panelText(t, open, "Detail"); strings.Contains(got, "…") || !strings.Contains(got, "no recorded result") || !hasWordsInOrder(got, holdWords) {
			t.Errorf("space did not lay the Detail panel out whole:\n%s", open)
		}
		if hint := strings.Join(strings.Fields(open), " "); !strings.Contains(hint, "space fold") {
			t.Errorf("the hint line does not offer space to fold:\n%s", open)
		}
		g.press("space")
		if folded := stripANSI(g.view()); folded != before {
			t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
		}
	}
}

// TestAClickOnATextPanesMarkExpandsThePane: a click on the "…" of a cut line of
// a pane with no rows gives the pane the focus and expands it, as space does;
// a click on another cell of the line only gives the focus.
func TestAClickOnATextPanesMarkExpandsThePane(t *testing.T) {
	t.Parallel()
	g := detailRig(t, 40, 50)
	g.tabTo("Tickets")
	frame := g.view()
	lines := strings.Split(stripANSI(frame), "\n")
	y, x := -1, -1
	in := false
	for i, l := range lines {
		in = in && !strings.Contains(l, "╰")
		if strings.Contains(l, "╭─ Detail") {
			in = true
		}
		if at := strings.Index(l, "…"); in && at >= 0 {
			y, x = i, len([]rune(l[:at]))
			break
		}
	}
	if y < 0 {
		t.Fatalf("no cut line in the Detail panel:\n%s", frame)
	}
	leftClick(g, 8, y)
	if g.m.focusedPane() != "Detail" || expandedIn(g) != 0 {
		t.Fatalf("a click beside the mark did not only focus the pane: focus %q, %d expanded", g.m.focusedPane(), expandedIn(g))
	}
	g.tabTo("Tickets")
	leftClick(g, x, y)
	if g.m.focusedPane() != "Detail" || expandedIn(g) != 1 {
		t.Fatalf("a click on the mark did not focus and expand the pane: focus %q, %d expanded", g.m.focusedPane(), expandedIn(g))
	}
	if got := panelText(t, g.view(), "Detail"); strings.Contains(got, "…") || !hasWordsInOrder(got, holdWords) {
		t.Errorf("the pane is not whole after the click:\n%s", g.view())
	}
	leftClick(g, x, y)
	if expandedIn(g) != 1 {
		t.Errorf("a click on an expanded pane folded it")
	}
}

// TestAFoldedTextPaneIsAsItWas: a pane expanded and folded again, or left with
// its screen and opened again, draws the frame it drew before.
func TestAFoldedTextPaneIsAsItWas(t *testing.T) {
	t.Parallel()
	g := detailRig(t, 40, 50)
	before := stripANSI(g.view())
	g.press("space")
	if stripANSI(g.view()) == before {
		t.Fatalf("space changed nothing:\n%s", before)
	}
	g.press("esc")
	g.openChart("checkout")
	g.tabTo("Detail")
	if again := stripANSI(g.view()); again != before {
		t.Errorf("a screen opened again is not folded:\n--- before\n%s\n--- again\n%s", before, again)
	}
}

// TestAClippedBodyLineRegistersItsCut: the publish checklist's body and changelog
// lines are clipped to the room the page has, the last line ending in the cut; it
// registers a mark with the whole text, and space on the checklist shows them
// whole, which folds back.
func TestAClippedBodyLineRegistersItsCut(t *testing.T) {
	t.Parallel()
	g := publishRig(t, 40, 40, 30)
	if len(g.m.page().lines) == 0 {
		t.Fatal("no page")
	}
	// The body and the changelog are the Detail's, read whole beside the list that
	// names them: what is left to clip is a line wider than the pane, and it still
	// carries its mark.
	var p page
	p.boxTop("Detail", "")
	p.boxUnder("    " + cutTitle(30) + " …")
	p.lines[len(p.lines)-1].cuts = []cutMark{{x: width("    "+cutTitle(30)+" …") - 1, value: "body line 40"}}
	p.boxEnd()
	marked := 0
	for _, l := range g.m.wrapLong(p).lines {
		marked += len(l.cuts)
	}
	if marked != 1 {
		t.Fatalf("a clipped line wider than the pane registered %d marks, want 1", marked)
	}
	g.tabTo("Detail · body")
	if text := pagesText(g.m.page()); !strings.Contains(text, "body line 1") {
		t.Errorf("the Detail does not read the body:\n%s", text)
	}
	if g.m.keysOn.cut {
		t.Errorf("the Detail is cut")
	}
}
