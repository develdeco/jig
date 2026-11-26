package tui

import (
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// briefSubsRig is the ticket screen of JIG-1 with the Brief step under the Steps
// cursor, so the sub-steps pane lists the brief's: drafted, slices written,
// approved. The status and the brief text are the ones given.
func briefSubsRig(t *testing.T, w, h int, status, text string) *rig {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", status, 0)
	g.r.answer("brief show JIG-1 --text --store /s", text, 0)
	g.feed(jig1Records()...).sized(w, h)
	g.view()
	g.press("up")
	if g.m.Screen() != "ticket" {
		t.Fatalf("on %s, want the ticket", g.m.Screen())
	}
	g.view()
	return g
}

// TestEachBriefSubstepOpensItsPartInOneEnter: a Brief sub-step is a row of the
// sub-steps pane, and enter on it opens the brief screen with the cursor on the
// part it is about: brief drafted on the first section, slices written on the
// first slice, brief approved on the approval.
func TestEachBriefSubstepOpensItsPartInOneEnter(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 40}, {100, 40}} {
		for _, c := range []struct{ sub, kind, id string }{
			{"drafted", rowSection, "section-1"},
			{"slices", rowSlice, "a"},
			{"approved", rowApproval, "approval"},
		} {
			g := briefSubsRig(t, size[0], size[1], statusJIG1Brief(), briefTextJIG1())
			g.tabTo("Brief").focus(c.sub)
			g.press("enter")
			if g.m.Screen() != "brief" {
				t.Fatalf("%v %s: enter opened %s, want the brief", size, c.sub, g.m.Screen())
			}
			g.view()
			if ref := g.m.selected(); ref == nil || ref.kind != c.kind || ref.id != c.id || g.m.focusedPane() != "Parts" {
				t.Errorf("%v %s: the brief opened with the cursor on %+v in %q, want %s %s in Parts", size, c.sub, ref, g.m.focusedPane(), c.kind, c.id)
			}
		}
	}
}

// noPartsStatus is the status of a ticket whose brief has no section and no
// slice yet.
func noPartsStatus() string {
	return "ticket: JIG-1\nstate: queued\n" + axi.KV("brief", [][2]string{{"approval", "none"}}) + "\n"
}

// TestASubstepWithNoPartOpensTheFirstRow: a sub-step whose part does not exist (no
// section yet, no slice) opens the brief screen at its first row, so one enter
// always opens something.
func TestASubstepWithNoPartOpensTheFirstRow(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 40}, {100, 40}} {
		for _, sub := range []string{"drafted", "slices"} {
			g := briefSubsRig(t, size[0], size[1], noPartsStatus(), briefTextJIG1())
			g.tabTo("Brief").focus(sub)
			g.press("enter")
			if g.m.Screen() != "brief" {
				t.Fatalf("%v %s: enter opened %s, want the brief", size, sub, g.m.Screen())
			}
			g.view()
			if ref := g.m.selected(); ref == nil || ref.kind != rowApproval {
				t.Errorf("%v %s: the brief opened on %+v, want its first row, the approval, the only part there is", size, sub, ref)
			}
		}

		// A second visit: the brief has sections and no slice; it was left on its
		// third section, and the sub-step with no part opens the first row, not
		// where the visit before left the cursor.
		status := noPartsStatus() + axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Goal", "-"}, {"Greeting", "-"}, {"Farewell", "-"}})
		g := briefSubsRig(t, size[0], size[1], status, briefTextJIG1())
		g.tabTo("Brief").focus("drafted")
		g.press("enter", "down", "down", "esc")
		g.view()
		g.tabTo("Brief").focus("slices")
		g.press("enter")
		g.view()
		if ref := g.m.selected(); g.m.Screen() != "brief" || ref == nil || ref.id != "section-1" {
			t.Errorf("%v: a sub-step with no part reopened the brief on %+v, want its first row, section-1", size, ref)
		}
	}
}

// TestEscFromAPartReturnsToTheSubstepItCameFrom: esc from the brief goes back to
// the ticket screen with the Brief pane focused and the sub-step still under its
// cursor.
func TestEscFromAPartReturnsToTheSubstepItCameFrom(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{80, 40}, {100, 40}} {
		g := briefSubsRig(t, size[0], size[1], statusJIG1Brief(), briefTextJIG1())
		g.tabTo("Brief").focus("slices")
		g.press("enter", "esc")
		g.view()
		if g.m.Screen() != "ticket" || g.m.focusedPane() != "Brief" {
			t.Fatalf("%v: esc went to %s with the focus on %q, want the ticket and Brief", size, g.m.Screen(), g.m.focusedPane())
		}
		if ref := g.m.selected(); ref == nil || ref.kind != rowSub || ref.id != "slices" {
			t.Errorf("%v: back on the ticket the cursor is on %+v, want the slices sub-step", size, ref)
		}
	}
}
