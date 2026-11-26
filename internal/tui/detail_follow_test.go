package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestTheDetailFollowsItsRowNotItsIndex: the Detail is of the row the cursor of
// the focused list stands on, found by the row's identity, so a zoom that keeps
// one pane, a search that narrows a list and a row that comes in above leave it
// on its own row.
func TestTheDetailFollowsItsRowNotItsIndex(t *testing.T) {
	t.Parallel()

	// A zoom: the Asks pane zoomed, tab to the Detail, which is the ask's.
	g := reviewFixRound(t, 100, 30)
	if got := g.view(); !strings.Contains(got, "╭─ Detail · r2-f2") {
		t.Fatalf("the review opens on the ask, whose detail is the Detail:\n%s", got)
	}
	g.press("z", "tab")
	if got := g.m.focusedPane(); got != "Detail · r2-f2" {
		t.Errorf("zoomed on the asks, tab reaches %q, want the ask's Detail", got)
	}
	g.press("z")
	if got := g.view(); !strings.Contains(got, "╭─ Detail · r2-f2") || strings.Contains(got, "╭─ Detail · round") {
		t.Errorf("unzoomed, the Detail is not the ask's:\n%s", got)
	}

	// A search that narrows the slices: the row it lights has its own Detail.
	s := tourRig(t, 100, 30)
	s.press("2").tabTo("Slices")
	s.press("/")
	typeText(s, "fix-1")
	if got := s.m.selected(); got == nil || got.slice != "fix-1-alpha" {
		t.Fatalf("the search puts the cursor on %+v, want fix-1-alpha", got)
	}
	if got := s.view(); !strings.Contains(got, "╭─ Detail · fix-1-alpha") {
		t.Errorf("the Detail is not of the row the search lit:\n%s", got)
	}

	// A round that comes in above, with the focus on another pane: the Detail
	// stays on the ask, whose pane keeps it.
	r := reviewFixRound(t, 100, 40)
	r.tabTo("Review activity")
	r.feed(
		jl(at(14, 0), "JIG-2", "", journal.EventGateOpen),
		jl(at(14, 30), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 3, "s-reviewer" }),
	)
	if got := r.view(); !strings.Contains(got, "╭─ Detail · r2-f2") {
		t.Errorf("a round that came in above moved the Detail off the ask:\n%s", got)
	}
}

// TestDownScrollsAScreenWithNoRows: on a screen whose focused pane has no row to
// move to, down reads the page a line at a time, whichever screen it is: the
// slice screen at 60x20, whose one row cannot move, and a page with no row at all.
func TestDownScrollsAScreenWithNoRows(t *testing.T) {
	t.Parallel()
	g := sized(newRig(t, "JIG-1"), 60, 20)
	g.feed(questionTicket()...)
	g.press("2")
	g.tabTo("Slices")
	for i := 0; i < 6 && (g.m.selected() == nil || g.m.selected().slice != "c"); i++ {
		g.press("down")
	}
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want the slice screen", g.m.Screen())
	}
	first := g.view()
	g.press("down", "down", "down", "down")
	if g.view() == first {
		t.Errorf("four downs on the slice screen leave the frame as it was:\n%s", first)
	}
}
