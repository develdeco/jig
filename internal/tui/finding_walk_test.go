package tui

import (
	"strings"
	"testing"
)

// onSlice opens the slice screen of id from the build screen.
func (g *rig) onSlice(id string) *rig {
	g.t.Helper()
	g.press("2")
	g.cursorOn(rowSlice, id)
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != id {
		g.t.Fatalf("on %s %q, want the slice %s", g.m.Screen(), g.m.top().slice, id)
	}
	return g
}

// TestAFixSlicesFindingPanelOpensItsFindingAndRound: a fix slice that answers two
// findings of round 1 has a row for the round and one for each finding in its
// Finding panel: the round opens the review screen on it, a finding opens its own
// screen, and esc returns to the slice each time.
func TestAFixSlicesFindingPanelOpensItsFindingAndRound(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {100, 30}} {
		g := threeRoundsFixing(t, size[0], size[1], 0, "r1-f1+r1-f2")
		g.onSlice("fix-1-alpha")
		g.tabTo("Finding")
		if got := strings.Join(g.refsOfKind(rowFinding), " "); got != "r1-f1 r1-f2" {
			t.Fatalf("%dx%d: the Finding panel has finding rows %q, want r1-f1 r1-f2:\n%s", size[0], size[1], got, g.view())
		}
		for _, id := range []string{"r1-f1", "r1-f2"} {
			g.cursorOn(rowFinding, id)
			g.press("enter")
			if g.m.Screen() != "finding" || g.m.top().finding != id {
				t.Fatalf("%dx%d: enter on %s opened %s %q", size[0], size[1], id, g.m.Screen(), g.m.top().finding)
			}
			g.press("esc")
			if g.m.Screen() != "slice" || g.m.top().slice != "fix-1-alpha" {
				t.Fatalf("%dx%d: esc from %s went to %s", size[0], size[1], id, g.m.Screen())
			}
			if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != id {
				t.Errorf("%dx%d: esc leaves the cursor on %+v, want %s", size[0], size[1], ref, id)
			}
		}
		g.cursorOn(rowRound, "1")
		g.press("enter")
		if g.m.Screen() != "review" {
			t.Fatalf("%dx%d: enter on the round opened %s, want the review", size[0], size[1], g.m.Screen())
		}
		if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "1" {
			t.Errorf("%dx%d: the review opens with the cursor on %+v, want round 1", size[0], size[1], ref)
		}
		g.press("esc")
		if g.m.Screen() != "slice" {
			t.Errorf("%dx%d: esc from the round went to %s, want the slice", size[0], size[1], g.m.Screen())
		}
	}
}

// TestTheWalkFromAFindingToItsSliceAndBackIsOneStack: round, finding, fix slice,
// finding and round are one stack of screens, and esc takes each back in turn.
func TestTheWalkFromAFindingToItsSliceAndBackIsOneStack(t *testing.T) {
	t.Parallel()
	g := threeRounds(t, 100, 30, 0)
	base := len(g.m.nav)
	g.cursorOn(rowRound, "1").cursorOn(rowFinding, "r1-f1")
	g.press("enter") // the finding
	g.cursorOn(rowSlice, "fix-1-alpha")
	g.press("enter") // its fix slice
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want the slice", g.m.Screen())
	}
	g.cursorOn(rowFinding, "r1-f1")
	g.press("enter") // the finding again, from the slice
	g.cursorOn(rowSlice, "fix-1-alpha")
	g.press("enter")
	g.cursorOn(rowRound, "1")
	g.press("enter") // round 1, from the slice
	want := []string{"finding", "slice", "finding", "slice", "review"}
	if got := len(g.m.nav) - base; got != len(want) {
		t.Fatalf("the walk is %d screens deep, want %d", got, len(want))
	}
	for i, w := range want {
		if got := g.m.nav[base+i].screen.String(); got != w {
			t.Errorf("screen %d of the walk is %s, want %s", i, got, w)
		}
	}
	for i := len(want) - 2; i >= 0; i-- {
		g.press("esc")
		if got := g.m.Screen(); got != want[i] {
			t.Fatalf("esc went to %s, want %s", got, want[i])
		}
	}
	g.press("esc")
	if g.m.Screen() != "review" || len(g.m.nav) != base {
		t.Errorf("the last esc is on %s at depth %d, want the review at %d", g.m.Screen(), len(g.m.nav), base)
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowFinding || ref.id != "r1-f1" {
		t.Errorf("the review keeps its cursor on %+v, want the finding the walk began on", ref)
	}
}

// TestAFailedSuiteRowOpensTheFixTaskItAddedTheNewestOfSeveralOrNone: on an issue's
// plan a failed suite row takes the cursor; enter opens the fix task it names, the
// newest of several, esc returns to the plan, and a failed row that names none
// opens nothing and says so.
func TestAFailedSuiteRowOpensTheFixTaskItAddedTheNewestOfSeveralOrNone(t *testing.T) {
	t.Parallel()
	row := func(fixes string) []string {
		return []string{`demo,fail,go test ./...,4d577e2aaaaaa,9,"final:boom",-,` + fixes + `,-`}
	}
	g := planRig(t, planSpec{suite: row("SHOP-3+SHOP-4")})
	g.m.height, g.m.width = 40, 100
	g.tabTo("Issue")
	if ref := g.m.selected(); ref == nil || ref.kind != rowSuite || ref.id != "SHOP-4" {
		t.Fatalf("tab into the Issue panel puts the cursor on %+v, want the failed suite row naming SHOP-4", ref)
	}
	g.press("enter")
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-4" {
		t.Fatalf("enter opened %s %q, want the ticket SHOP-4", g.m.Screen(), g.m.top().ticket)
	}
	g.press("esc")
	if g.m.Screen() != "chart" {
		t.Errorf("esc went to %s, want the plan", g.m.Screen())
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowSuite {
		t.Errorf("esc leaves the cursor on %+v, want the suite row", ref)
	}

	none := planRig(t, planSpec{suite: row("-")})
	none.m.height, none.m.width = 40, 100
	none.tabTo("Issue")
	none.press("enter")
	if none.m.Screen() != "chart" || !strings.Contains(none.m.notice, "no fix task") {
		t.Errorf("a failed row with no fix task: on %s, notice %q, want the plan and the reason", none.m.Screen(), none.m.notice)
	}

	passed := planRig(t, planSpec{suite: []string{"demo,pass,go test ./...,9999999aaaaaa,12,-,-,-,-"}})
	passed.m.height, passed.m.width = 40, 100
	if refs := passed.refsOfKind(rowSuite); len(refs) != 0 {
		t.Errorf("a passed suite row takes the cursor: %v", refs)
	}
}
