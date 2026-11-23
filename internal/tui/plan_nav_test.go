package tui

import (
	"strings"
	"testing"
)

// onRow moves the cursor of the screen on top to the row of ticket id.
func onRow(t *testing.T, g *rig, id string) {
	t.Helper()
	g.press("home")
	for i := 0; i < 40; i++ {
		if ref := g.m.selected(); ref != nil && ref.id == id {
			return
		}
		g.press("down")
	}
	t.Fatalf("no row of %s:\n%s", id, g.view())
}

// TestAnIssueOpensItsPlanFromMainAndTheChartRow: enter on an issue's row on the
// project's list opens its plan, the screen of the chart that plans it, and so
// does enter on the chart's row; esc returns to the list from either.
func TestAnIssueOpensItsPlanFromMainAndTheChartRow(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{})
	onRow(t, g, "GATE-49")
	g.press("enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Fatalf("enter on the issue opened %s of %q, want its plan:\n%s", g.m.Screen(), g.m.top().chart, g.view())
	}
	v := stripANSI(g.view())
	for _, want := range []string{"Need", "Tasks", "Issue", "T-1", "landed"} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan lacks %q:\n%s", want, v)
		}
	}
	for _, bad := range []string{"1 Brief", "2 Build", "3 Review", "4 Publish"} {
		if strings.Contains(v, bad) {
			t.Errorf("the plan has the task-shaped screen's %q:\n%s", bad, v)
		}
	}
	g.press("esc")
	if g.m.Screen() != "main" {
		t.Fatalf("esc went to %s, want main", g.m.Screen())
	}
	g.press("end", "enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" || !strings.Contains(stripANSI(g.view()), "Need") {
		t.Errorf("enter on the chart row opened %s of %q:\n%s", g.m.Screen(), g.m.top().chart, g.view())
	}
	g.press("esc")
	if g.m.Screen() != "main" {
		t.Errorf("esc went to %s, want main", g.m.Screen())
	}

	// An issue no chart plans keeps its ticket screen.
	lone := planMain(t, planSpec{})
	lone.m.w.charts["repeat"].listing.Issue = ""
	onRow(t, lone, "GATE-49")
	lone.press("enter")
	if lone.m.Screen() != "ticket" {
		t.Errorf("an issue no chart plans opened %s, want its ticket screen", lone.m.Screen())
	}
}

// TestCOpensATasksIssueAndEscReturns: c on a task's screen, or a step's, opens
// the plan of its issue; a task two issues list opens the one it was reached
// through, else the first, and says both in its header; a task no issue lists
// has no c in its hints and does nothing when it is pressed. esc returns.
func TestCOpensATasksIssueAndEscReturns(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{loose: true})
	onRow(t, g, "T-2")
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "T-2" {
		t.Fatalf("enter opened %s of %q", g.m.Screen(), g.m.top().ticket)
	}
	if v := stripANSI(g.view()); !strings.Contains(v, "c issue") {
		t.Errorf("the task's hints lack c:\n%s", v)
	}
	g.press("c")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" || !strings.Contains(stripANSI(g.view()), "Need") {
		t.Fatalf("c opened %s of %q, want the plan of GATE-49:\n%s", g.m.Screen(), g.m.top().chart, g.view())
	}
	g.press("esc")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "T-2" {
		t.Errorf("esc went to %s of %q, want the task's screen again", g.m.Screen(), g.m.top().ticket)
	}
	// From a step's screen as well.
	g.press("enter") // the current step of the task
	if g.m.Screen() == "ticket" {
		t.Fatalf("enter on a step stayed on the ticket screen")
	}
	g.press("c")
	if g.m.Screen() != "chart" || g.m.top().chart != "repeat" {
		t.Errorf("c on a step's screen opened %s of %q", g.m.Screen(), g.m.top().chart)
	}

	// A task two issues list: reached through the second plan, c opens the second.
	s := planMain(t, planSpec{second: true})
	s.press("end", "enter") // the last chart row: "second"
	if s.m.top().chart != "second" {
		t.Fatalf("the last chart row is %q, want second", s.m.top().chart)
	}
	s.press("enter")
	if s.m.top().ticket != "T-1" {
		t.Fatalf("enter opened %q, want T-1", s.m.top().ticket)
	}
	if v := stripANSI(s.view()); !strings.Contains(v, "task of GATE-49 and GATE-50") {
		t.Errorf("the header of a shared task does not name both issues:\n%s", v)
	}
	s.press("c")
	if s.m.top().chart != "second" {
		t.Errorf("c opened %q, want the plan it was reached through, second", s.m.top().chart)
	}
	// Reached from the list, with no plan on the way, c opens the first in chart order.
	s2 := planMain(t, planSpec{second: true})
	onRow(t, s2, "T-1")
	s2.press("enter", "c")
	if s2.m.top().chart != "repeat" {
		t.Errorf("c from the list opened %q, want the first plan, repeat", s2.m.top().chart)
	}

	// A task of no issue.
	l := planMain(t, planSpec{loose: true})
	onRow(t, l, "T-9")
	l.press("enter")
	if v := stripANSI(l.view()); strings.Contains(v, "c issue") {
		t.Errorf("a task of no issue offers c:\n%s", v)
	}
	l.press("c")
	if l.m.Screen() != "ticket" || l.m.top().ticket != "T-9" {
		t.Errorf("c on a task of no issue went to %s of %q", l.m.Screen(), l.m.top().ticket)
	}
}

// TestMTogglesTheMapOnThePlan: the plan hides the chart's map until m shows it
// in place, m hides it again, and the hint says so; m does nothing on a screen
// that is no plan.
func TestMTogglesTheMapOnThePlan(t *testing.T) {
	t.Parallel()
	g := planRig(t, planSpec{})
	g.m.height = 40
	if v := stripANSI(g.view()); strings.Contains(v, "Map") || !strings.Contains(v, "m map") {
		t.Fatalf("the plan shows the map before m, or hides the hint:\n%s", v)
	}
	g.press("m")
	if g.m.Screen() != "chart" {
		t.Fatalf("m left the plan for %s", g.m.Screen())
	}
	if v := stripANSI(g.view()); !strings.Contains(v, "Map") || !strings.Contains(v, "Plans issue GATE-49.") || !strings.Contains(v, "Need") {
		t.Errorf("m did not show the map beside the plan:\n%s", v)
	}
	g.press("m")
	if v := stripANSI(g.view()); strings.Contains(v, "Map") {
		t.Errorf("a second m left the map shown:\n%s", v)
	}

	// The legacy chart has no map to toggle, and its hints do not say so.
	c := chartScreenRig(t)
	c.press("m")
	if v := stripANSI(c.view()); strings.Contains(v, "m map") {
		t.Errorf("a chart that plans no issue offers m:\n%s", v)
	}
}
