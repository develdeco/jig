package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestMainColumnsAreMeasuredByKind: a chart's name is not a ticket's id, so a
// 47-cell chart name leaves the id column at the widest ticket id, and at 100 by
// 30 a ticket's title fits one row beside its steps.
func TestMainColumnsAreMeasuredByKind(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	const long = "a-chart-whose-name-is-as-long-as-a-sentence-word" // 47 cells
	c := g.m.w.charts["checkout"]
	delete(g.m.w.charts, "checkout")
	g.m.w.charts[long] = c
	g.m.w.chartOrder = []string{long}
	g.m.w.gen++
	g.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	view := stripANSI(g.view())
	if !strings.Contains(view, long) {
		t.Fatalf("the long chart's name is not drawn:\n%s", view)
	}
	row := lineWith(view, "JIG-5")
	if !strings.Contains(row, "JIG-5  Apply a coupon code  ") {
		t.Errorf("the id column is widened by the chart's name, or the title wraps: %q\n%s", row, view)
	}
}

// TestMoreCountsTheRowsItHides: the marks of a window count the rows it hides in
// the box they sit in, a row being its line and the lines its text wrapped onto,
// and no edge or loose line outside the box.
func TestMoreCountsTheRowsItHides(t *testing.T) {
	t.Parallel()
	var pg page
	for i := 0; i < 3; i++ {
		pg.add(fmt.Sprintf("loose %d", i))
	}
	pg.boxTop("Box", "")
	for i := 0; i < 6; i++ {
		pg.boxLaid(nil, []string{fmt.Sprintf("row %d", i), "  wrapped"})
	}
	pg.boxEnd()
	pg.add("after 1")
	pg.add("after 2")
	g := newRig(t, "")
	g.send(tea.WindowSizeMsg{Width: 40, Height: 30})
	// Lines 8 to 11 are rows 2 and 3 of the box: two rows hidden above, two below.
	rows, from, to := g.m.windowRows(pg, 0, 8, 8, 40, "")
	if from != 8 || to != 12 || !strings.Contains(rows[1], "↑ 2 more") || !strings.Contains(rows[6], "↓ 2 more") {
		t.Errorf("the window is lines %d to %d:\n%s", from, to, strings.Join(rows, "\n"))
	}
	// Loose rows that wrapped above a window outside any box count their rows, not
	// their lines.
	var loose page
	for i := 0; i < 10; i++ {
		loose.addLaid(nil, []string{fmt.Sprintf("row %d", i), "  wrapped"})
	}
	// Lines 6 to 9 are rows 3 and 4: three rows above, five below.
	rows, _, _ = g.m.windowRows(loose, 0, 6, 6, 40, "")
	if !strings.Contains(rows[0], "↑ 3 more") || !strings.Contains(rows[len(rows)-1], "↓ 5 more") {
		t.Errorf("the window of loose rows:\n%s", strings.Join(rows, "\n"))
	}
}

// TestAStepsClocksAlwaysShow: at 46 columns the time a step took, the clock of
// its wait or idleness and the open sub-step's clock are drawn whole, and the
// names and words give way.
func TestAStepsClocksAlwaysShow(t *testing.T) {
	t.Parallel()
	g := longStepsRig(t, 46, 40, [3]string{"round-totals", "fix-4-cart-test", "cart-total"}, false)
	view := stripANSI(g.view())
	row := func(start string) string {
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, start) {
				return l
			}
		}
		return ""
	}
	for start, clocks := range map[string][]string{"◐ 2": {"1m 10s", "idle since 09:02"}, "◐ slice": {"1m 03s"}} {
		for _, clock := range clocks {
			if line := row(start); !strings.Contains(line, clock) {
				t.Errorf("the clock %q is cut at 46 columns from %q:\n%s", clock, line, view)
			}
		}
	}
}

// TestTheOpenSubstepsClockShowsWhateverItsLabelIs: a label too long for the
// open sub-step's line gives way to the clock at its end.
func TestTheOpenSubstepsClockShowsWhateverItsLabelIs(t *testing.T) {
	t.Parallel()
	g := longStepsRig(t, 34, 40, [3]string{"round-totals", "fix-4-cart-test", "cart-total"}, false)
	for _, l := range strings.Split(stripANSI(g.view()), "\n") {
		if strings.Contains(l, "◐ slice") && !strings.Contains(l, "1m 03s") {
			t.Errorf("the open sub-step's clock is cut: %q", l)
		}
	}
}

// TestADownArrowOnMainMeasuresNoRow: the project's list is laid out when it is
// built for what it reads, so a down arrow, which moves the cursor and nothing
// else, and a frame that changed nothing measure no row.
func TestADownArrowOnMainMeasuresNoRow(t *testing.T) {
	t.Parallel()
	g := manyTickets(t, 100, 30)
	g.view()
	if g.m.measured == 0 {
		t.Fatal("the first frame measured no row: the counter counts nothing")
	}
	before := g.m.measured
	g.view()
	g.press("down")
	g.view()
	g.press("down", "down")
	g.view()
	if g.m.measured != before {
		t.Errorf("a down arrow and an idle frame measured %d rows", g.m.measured-before)
	}
}
