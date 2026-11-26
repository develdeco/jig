package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// bigSlices is the build screen of a ticket of twelve slices s01 to s12, the one
// numbered at9 titled with a goal of n words.
func bigSlices(t *testing.T, w, h, at9, n int) *rig {
	t.Helper()
	var rows [][]string
	for i := 1; i <= 12; i++ {
		goal := fmt.Sprintf("slice %02d goal", i)
		if i == at9 {
			goal = cutTitle(n)
		}
		rows = append(rows, []string{fmt.Sprintf("s%02d", i), "queued", "0", "-", "-", goal})
	}
	status := strings.Join([]string{"ticket: BIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows), "questions: none",
	}, "\n") + "\n"
	g := newRig(t, "BIG-1")
	g.r.answer("status BIG-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(jl(at(1, 0), "BIG-1", "s01", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	g.press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	return g
}

// TestARowUnderAMoreMarkExpandsAndIsReadToItsEnd: a list with more rows than its
// window holds counts the ones it leaves out; a cut row below such a count is
// still the cursor's, space is listed and opens it, and it is read to its end.
func TestARowUnderAMoreMarkExpandsAndIsReadToItsEnd(t *testing.T) {
	t.Parallel()
	for _, at := range []int{9, 10} {
		for _, n := range []int{60, 300} {
			g := bigSlices(t, 80, 24, at, n)
			g.tabTo("Slices")
			for i := 1; i < at; i++ {
				g.press("down")
			}
			id := fmt.Sprintf("s%02d", at)
			frame := stripANSI(g.view())
			if atCursor(g) != id || !strings.Contains(strings.Join(strings.Fields(frame), " "), "space expand") {
				t.Fatalf("s%02d n=%d: the cut row under the cursor does not list space (cursor on %s):\n%s", at, n, atCursor(g), frame)
			}
			g.press("space")
			last := fmt.Sprintf("w%03d", n)
			open := stripANSI(g.view())
			for i := 0; i < 400 && !strings.Contains(open, last) && atCursor(g) == id; i++ {
				g.press("down")
				open = stripANSI(g.view())
			}
			if !strings.Contains(open, last) || atCursor(g) != id {
				t.Errorf("s%02d n=%d: not read to %s (cursor on %s):\n%s", at, n, last, atCursor(g), open)
			}
		}
	}
}

// outsideFrame is the frame with the lines of the pane titled title taken out.
func outsideFrame(frame, title string) []string {
	in := false
	var out []string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "╭─ "+title) {
			in = true
		}
		if !in && !strings.Contains(l, "space ") {
			out = append(out, l)
		}
		if in && strings.Contains(l, "╰") {
			in = false
		}
	}
	return out
}

// TestAnExpansionKeepsTheFoldedFrameOutsideItsPane: an expanded row is read to its
// end, and one press past it, inside the pane it is in: every line outside that
// pane equals the folded frame's.
func TestAnExpansionKeepsTheFoldedFrameOutsideItsPane(t *testing.T) {
	t.Parallel()
	check := func(name, pane string, g *rig) {
		t.Helper()
		folded := stripANSI(g.view())
		g.press("space")
		start := atCursor(g)
		for i := 0; i < 80 && atCursor(g) == start; i++ {
			frame := stripANSI(g.view())
			if want, got := outsideFrame(folded, pane), outsideFrame(frame, pane); strings.Join(want, "\n") != strings.Join(got, "\n") {
				t.Errorf("%s: the frame outside %s moved after %d presses:\n--- folded\n%s\n--- now\n%s", name, pane, i, folded, frame)
				return
			}
			g.press("down")
		}
	}
	for _, size := range [][2]int{{60, 20}, {80, 23}, {80, 24}} {
		g := longCard(t, size[0], size[1])
		for i := 0; i < 12; i++ {
			g.feed(toolOn("SHOP-1", at(3, i), "s-b", "git commit -m "+cutTitle(60), exit(0)))
		}
		check(fmt.Sprintf("activity %v", size), "Activity", g.tabTo("Activity"))
	}
	check("main", "Ready", cutRig(t, 80, 24, 60).onRow("JIG-1"))
	check("a phase list", "Slices", bigSlices(t, 80, 24, 3, 200).tabTo("Slices").press("down", "down"))
}

// TestCollapsedListsTakeWhatTheCursorsListLeaves: a list the cursor is not in,
// collapsed to its edges, draws its first row when the cursor's list leaves the
// lines for it, instead of leaving them blank.
func TestCollapsedListsTakeWhatTheCursorsListLeaves(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 80, 24)
	frame := stripANSI(g.view())
	rounds := panelText(t, frame, "Rounds")
	if !strings.Contains(rounds, "round 2") {
		t.Errorf("the Rounds list is a bare box with lines left over:\n%s", frame)
	}
	if strings.Contains(frame, "\n\n\n") {
		t.Errorf("lines are left blank above the keys:\n%s", frame)
	}
}
