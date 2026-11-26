package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// longCard is the ticket in the middle of its build of cardRig with a goal of 400
// cells on the slice under way, and as its latest move a command of 150 cells.
func longCard(t *testing.T, width, height int) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
			{"clamp", "building", "1", "round", "-", cutTitle(80)},
			{"cart", "queued", "0", "clamp", "-", "Show the cart"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: width, Height: height})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		jl(at(1, 0), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(1, 30), "SHOP-1", "round", journal.EventVerifyPending, with("s-b", sha('a'), "", "Round to the cent", "", "")),
		jl(at(1, 37), "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(2, 0), "SHOP-1", "clamp", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(2, 10), "SHOP-1", "clamp", journal.EventVerifyPending, with("s-b", sha('b'), "", "Clamp negative quantities", "", "")),
		toolOn("SHOP-1", at(2, 30), "s-b", "git commit -m "+cutTitle(30), exit(0)),
	)
	return g
}

// paneBlock is the text of the pane titled title on the frame, its words.
func paneBlock(t *testing.T, frame, title string) string {
	t.Helper()
	return panelText(t, frame, title)
}

// TestAnActivityLineExpandsToItsWholeText: on the ticket screen tab reaches the
// Activity pane, whose newest line is cut; space shows the command whole, and
// space again folds it back.
func TestAnActivityLineExpandsToItsWholeText(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 50}, {60, 70}} {
		g := longCard(t, size[0], size[1])
		g.tabTo("Activity")
		before := stripANSI(g.view())
		if newest := between(paneBlock(t, before, "Activity"), "09:02:30", "09:02:10"); !strings.Contains(newest, "…") || hasWordsInOrder(newest, cutTitle(30)) {
			t.Fatalf("%dx%d: the newest line is not cut:\n%s", size[0], size[1], before)
		}
		if hint := strings.Join(strings.Fields(before), " "); !strings.Contains(hint, "space expand") {
			t.Errorf("the hint line does not offer space:\n%s", before)
		}
		g.press("space")
		open := stripANSI(g.view())
		if newest := between(paneBlock(t, open, "Activity"), "09:02:30", "09:02:10"); strings.Contains(newest, "…") || !hasWordsInOrder(newest, cutTitle(30)) {
			t.Errorf("%dx%d: space did not show the line whole:\n%s", size[0], size[1], open)
		}
		g.press("space")
		if folded := stripANSI(g.view()); folded != before {
			t.Errorf("%dx%d: folding does not give the frame back:\n--- before\n%s\n--- after\n%s", size[0], size[1], before, folded)
		}
		// A click on the mark does what space does.
		lines := strings.Split(before, "\n")
		for y, l := range lines {
			if at := strings.Index(l, "…"); at >= 0 && y > 0 && strings.Contains(strings.Join(lines[:y], "\n"), "╭─ Activity") {
				leftClick(g, len([]rune(l[:at])), y)
				break
			}
		}
		if expandedIn(g) != 1 {
			t.Errorf("%dx%d: a click on the mark did not expand the line", size[0], size[1])
		}
	}
}

// TestASubstepExpandsItsCutFieldsWhole: the sub-step that is open on the build
// step shows its goal cut to three rows; space on it gives every field all the
// rows it needs, and folds back.
func TestASubstepExpandsItsCutFieldsWhole(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 60}, {60, 70}} {
		g := longCard(t, size[0], size[1])
		g.tabTo("Build")
		before := stripANSI(g.view())
		build := paneBlock(t, before, "Build")
		if !strings.Contains(build, "goal") || !strings.Contains(build, "…") || hasWordsInOrder(build, cutTitle(80)) {
			t.Fatalf("%dx%d: the goal is not cut:\n%s", size[0], size[1], before)
		}
		g.press("space")
		open := stripANSI(g.view())
		// Read by lines inside the room the pane has: the goal is drawn whole when
		// the pane holds it, else by lines down to the live line's last word.
		for i := 0; i < 40 && !strings.Contains(paneBlock(t, open, "Build"), "w030"); i++ {
			g.press("down")
			open = stripANSI(g.view())
		}
		if build := paneBlock(t, open, "Build"); !strings.Contains(build, "w030") || strings.Contains(between(build, "goal", "held by"), "…") {
			t.Errorf("%dx%d: the expanded sub-step is not read to its live line:\n%s", size[0], size[1], open)
		}
		for len(g.m.top().expanded) > 0 && g.m.top().rowScroll > 0 {
			g.press("up")
		}
		g.press("space")
		if folded := stripANSI(g.view()); folded != before {
			t.Errorf("%dx%d: folding does not give the frame back:\n--- before\n%s\n--- after\n%s", size[0], size[1], before, folded)
		}
	}
}

// TestAChartActivityLineExpandsToItsWholeText: the chart screen's Activity pane
// has a line for every ticket's moves; its cut line expands to its whole text.
func TestAChartActivityLineExpandsToItsWholeText(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	g.send(tea.WindowSizeMsg{Width: 80, Height: 60})
	g.feed(toolOn("JIG-5", at(20, 0), "s-builder", "git commit -m "+cutTitle(30), exit(0)))
	onChart(g)
	g.tabTo("Activity")
	before := stripANSI(g.view())
	if activity := paneBlock(t, before, "Activity"); !strings.Contains(activity, "…") {
		t.Fatalf("no line of the chart's activity is cut:\n%s", before)
	}
	g.press("space")
	open := stripANSI(g.view())
	if newest := between(paneBlock(t, open, "Activity"), "09:20:00", "09:14:21"); strings.Contains(newest, "…") || !hasWordsInOrder(newest, cutTitle(30)) {
		t.Errorf("space did not show the line whole:\n%s", open)
	}
	g.press("space")
	if folded := stripANSI(g.view()); folded != before {
		t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
	}
}
