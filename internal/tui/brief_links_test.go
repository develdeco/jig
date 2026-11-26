package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// linkSizes are the sizes the links are proved at.
var linkSizes = [][2]int{{80, 24}, {100, 30}}

// onSlice is the screen the rig is on and the slice it names.
func onSlice(g *rig) string { return g.m.Screen() + " " + g.m.top().slice }

// TestEnterOnASliceASectionCitesOpensIt: in the detail of a section the slices
// that cite it are rows, the cursor reaches them with tab, and enter on one opens
// its slice screen; the section's own row opens nothing.
func TestEnterOnASliceASectionCitesOpensIt(t *testing.T) {
	t.Parallel()
	for _, size := range linkSizes {
		g := briefRigWith(t, size[0], size[1], statusJIG1Brief(), briefTextJIG1()).focus("section-4")
		g.press("enter")
		if g.m.Screen() != "brief" {
			t.Fatalf("%v: the section row opened %s", size, g.m.Screen())
		}
		g.tabTo("Detail · Tone")
		if ref := g.m.selected(); ref == nil || ref.kind != rowStop {
			t.Fatalf("%v: the cursor in the detail of Tone begins on %+v, want its first line, which opens nothing", size, ref)
		}
		g.press("enter")
		if g.m.Screen() != "brief" {
			t.Fatalf("%v: a line of the detail that cites nothing opened %s", size, g.m.Screen())
		}
		g.press("down")
		if ref := g.m.selected(); ref == nil || ref.kind != rowSlice || ref.id != "c" {
			t.Fatalf("%v: the cursor in the detail of Tone is on %+v, want the first slice that cites it, c", size, ref)
		}
		g.press("enter")
		if got := onSlice(g); got != "slice c" {
			t.Errorf("%v: enter on the first cited slice opened %q, want slice c", size, got)
		}
		g.press("esc")
		if g.m.Screen() != "brief" {
			t.Errorf("%v: esc from the slice went to %s", size, g.m.Screen())
		}
		// Zoomed, the detail is whole and its second row is there to stand on.
		g.press("z", "down", "enter") // the cursor is on c; one down is d
		if got := onSlice(g); got != "slice d" {
			t.Errorf("%v: enter on the second cited slice opened %q, want slice d", size, got)
		}
	}
}

// TestASliceScreensSectionsAreRowsThatOpenTheBriefAtThem: a slice screen lists
// the sections that cite the slice, and enter on one opens the brief with its
// cursor on that section, whatever the brief's cursor was left on before.
func TestASliceScreensSectionsAreRowsThatOpenTheBriefAtThem(t *testing.T) {
	t.Parallel()
	for _, size := range linkSizes {
		g := briefRigWith(t, size[0], size[1], sinceStatus("same", "changed", "same", "same"), briefTextJIG1())
		g.focus("a").press("enter")
		if got := onSlice(g); got != "slice a" {
			t.Fatalf("%v: opened %q, want slice a", size, got)
		}
		view := g.view()
		panel := readWords(between(view, "╭─ Brief sections", "╰"))
		if !strings.Contains(panel, "Greeting changed since the approval") || strings.Contains(panel, "Tone") {
			t.Errorf("%v: slice a's Brief sections panel is not the section that cites it:\n%s", size, view)
		}
		g.tabTo("Brief sections")
		if ref := g.m.selected(); ref == nil || ref.kind != rowSection || ref.id != "section-2" {
			t.Fatalf("%v: the cursor is on %+v, want the section Greeting", size, ref)
		}
		g.press("enter")
		if g.m.Screen() != "brief" || g.m.selected() == nil || g.m.selected().id != "section-2" {
			t.Errorf("%v: enter opened %s with the cursor on %+v, want the brief at Greeting", size, g.m.Screen(), g.m.selected())
		}
		if !strings.Contains(g.view(), "╭─ Detail · Greeting") {
			t.Errorf("%v: the brief opened at the section does not show it:\n%s", size, g.view())
		}
	}
}

// TestEscFromTheBriefAtASectionReturnsToTheSliceScreen: the brief opened from a
// slice screen is one more screen on the stack: esc goes back to the slice
// screen and a second esc to the brief it came from.
func TestEscFromTheBriefAtASectionReturnsToTheSliceScreen(t *testing.T) {
	t.Parallel()
	for _, size := range linkSizes {
		g := briefRigWith(t, size[0], size[1], statusJIG1Brief(), briefTextJIG1())
		depth := len(g.m.nav)
		g.focus("d").press("enter").tabTo("Brief sections").press("enter")
		if g.m.Screen() != "brief" || len(g.m.nav) != depth+2 {
			t.Fatalf("%v: on %s with %d screens, want the brief pushed on the slice screen (%d)", size, g.m.Screen(), len(g.m.nav), depth+2)
		}
		g.press("esc")
		if got := onSlice(g); got != "slice d" {
			t.Errorf("%v: first esc went to %q, want slice d", size, got)
		}
		g.press("esc")
		if g.m.Screen() != "brief" || len(g.m.nav) != depth {
			t.Errorf("%v: second esc went to %s with %d screens, want the brief it came from", size, g.m.Screen(), len(g.m.nav))
		}
	}
}

// TestTheBriefOpensOnItsFirstRowWhenOpenedForNoPart: the step key, and the row
// of the ticket screen, open the brief on its first part.
func TestTheBriefOpensOnItsFirstRowWhenOpenedForNoPart(t *testing.T) {
	t.Parallel()
	for _, size := range linkSizes {
		g := newRig(t, "JIG-1")
		g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
		g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
		g.feed(jig1Records()...).press("up", "enter").sized(size[0], size[1])
		g.press("2", "1")
		if g.m.Screen() != "brief" {
			t.Fatalf("%v: on %s, want brief", size, g.m.Screen())
		}
		g.view()
		if ref := g.m.selected(); ref == nil || ref.id != "section-1" || g.m.focusedPane() != "Parts" {
			t.Errorf("%v: the brief opened on %+v in %q, want the first section in Parts", size, ref, g.m.focusedPane())
		}
	}
}

// TestTwoSectionsOfOneHeadingAreToldByPlace: a brief that repeats a heading has
// a row for each, a slice screen opens the brief at the one that cites it, and
// the other is not the one the cursor lands on.
func TestTwoSectionsOfOneHeadingAreToldByPlace(t *testing.T) {
	t.Parallel()
	status := strings.Replace(statusJIG1Brief(),
		"  Goal,-\n  Greeting,a\n  Farewell,b\n  Tone,c+d\n",
		"  Notes,a\n  Notes,b\n  Notes,c+d\n  Goal,-\n", 1)
	text := strings.Join([]string{
		axi.KV("brief", [][2]string{{"ticket", "JIG-1"}, {"title", "Greeting copy"}, {"illustration", "no"}}),
		axi.Table("sections", []string{"heading", "summary"}, [][]string{{"Notes", "First notes."}, {"Notes", "Second notes."}, {"Notes", "Third notes."}, {"Goal", "The goal."}}),
		"section: Notes\n  First notes.", "section: Notes\n  Second notes.", "section: Notes\n  Third notes.", "section: Goal\n  The goal.",
		axi.Help("serve it"),
	}, "\n") + "\n"
	for _, size := range linkSizes {
		g := briefRigWith(t, size[0], size[1], status, text)
		g.focus("b").press("enter").tabTo("Brief sections").press("enter")
		if ref := g.m.selected(); g.m.Screen() != "brief" || ref == nil || ref.id != "section-2" {
			t.Errorf("%v: slice b's section opened the brief with the cursor on %+v, want the second Notes", size, ref)
		}
		g.sized(100, 60)
		if d := readWords(between(g.view(), "╭─ Detail · Notes", "╰")); !strings.Contains(d, "Second notes.") || strings.Contains(d, "First notes.") {
			t.Errorf("%v: the detail is not the text of the second Notes:\n%s", size, g.view())
		}
	}
}
