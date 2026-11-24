package tui

import (
	"strings"
	"testing"
)

// TestAPlanHeaderCountsWhatNeedsYouAsItsRowsDo: a task two issues list that waits
// to land into the second issue is the second plan's need and not the first's,
// whose tasks all landed: each plan's header counts what needs the person through
// the same cut of each task as the plan's own rows, and the project's row of the
// chart says what its plan says.
func TestAPlanHeaderCountsWhatNeedsYouAsItsRowsDo(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{second: true})
	g.m.height, g.m.width = 40, 110

	onRow(t, g, "SHOP-49")
	g.press("enter")
	first := stripANSI(g.view())
	if head := strings.Split(first, "\n")[0]; strings.Contains(head, "needs you") {
		t.Errorf("the first issue's plan reads a landing into the second as its need: %q", head)
	}
	if row := lineWith(first, "SHOP-1  "); !strings.Contains(row, "landed") {
		t.Fatalf("the fixture's task did not land in the first issue: %q", row)
	}
	g.press("esc")

	g.openChart("second")
	second := stripANSI(g.view())
	if head := strings.Split(second, "\n")[0]; !strings.Contains(head, "needs you") {
		t.Errorf("the second issue's plan does not read the landing that waits for the person: %q", head)
	}
}
