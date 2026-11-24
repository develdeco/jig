package tui

import (
	"strings"
	"testing"
)

// byChartRig is the plan fixture with a second issue, a task no chart lists
// (SHOP-9) and a chart that plans no issue, listing SHOP-2 too.
func byChartRig(t *testing.T) *rig {
	t.Helper()
	g := planMain(t, planSpec{issue: waitingIssue, loose: true, second: true})
	g.m.height, g.m.width = 40, 110
	var listed []chartListing
	for _, name := range g.m.w.chartOrder {
		listed = append(listed, g.m.w.charts[name].listing)
	}
	g.m.w.setCharts(append(listed, chartListing{Name: "plain", Title: "A chart with no issue", Tickets: []string{"SHOP-2", "SHOP-3"}}))
	return g
}

// TestMainByChartHeadsItsTickets: s lays the list out by chart: an issue's chart
// is its issue row with its tasks, a chart with no issue is its chart row with
// its tickets under it, in the store's order; a ticket in no chart comes last
// under its own heading; the header and the hint line name the view; and s again
// brings the list by state back.
func TestMainByChartHeadsItsTickets(t *testing.T) {
	t.Parallel()
	g := byChartRig(t)
	g.unfold()
	if hint := stripANSI(strings.Join(g.m.footer(110), "\n")); !strings.Contains(hint, "s by chart") {
		t.Errorf("the hint line does not offer the view by chart:\n%s", hint)
	}
	g.press("s")
	text := stripANSI(g.mainText())
	// SHOP-1 is under both issues, SHOP-2 and SHOP-3 under the issue and the chart.
	want := "SHOP-49 SHOP-1 SHOP-2 SHOP-3 SHOP-4 SHOP-50 SHOP-1 SHOP-2 SHOP-3 SHOP-9"
	if got := strings.Join(rowIDs(text), " "); got != want {
		t.Errorf("the rows read %s, want %s:\n%s", got, want, text)
	}
	if !strings.Contains(text, "plain") || strings.Index(text, "plain") < strings.Index(text, "SHOP-50") || !strings.Contains(text, "no chart (1)") || strings.Index(text, "no chart (1)") < strings.Index(text, "SHOP-3") {
		t.Errorf("the ticket in no chart does not come last under its heading:\n%s", text)
	}
	if head := g.m.header(110)[0]; !strings.Contains(head, "by chart") {
		t.Errorf("the header does not name the view: %q", head)
	}
	if hint := stripANSI(strings.Join(g.m.footer(110), "\n")); !strings.Contains(hint, "s by state") {
		t.Errorf("the hint line does not offer the view by state:\n%s", hint)
	}
	// A chart row opens its chart, an issue its plan.
	g.onRow("plain").press("enter")
	if g.m.Screen() != "chart" || g.m.top().chart != "plain" {
		t.Errorf("enter on a chart's row opened %s of %q", g.m.Screen(), g.m.top().chart)
	}
	g.press("esc", "s")
	if head := g.m.header(110)[0]; strings.Contains(head, "by chart") {
		t.Errorf("s again left the view by chart: %q", head)
	}
	// The view is not kept: a new model opens by state.
	if fresh := byChartRig(t); fresh.m.nav[0].byChart {
		t.Errorf("a run opens by chart")
	}
}

// TestMainSwitchKeepsTheCursorOnItsTicket: the cursor stays on its ticket across
// the switch, under the same container where the view has it, and on its fold
// row when the ticket is folded away.
func TestMainSwitchKeepsTheCursorOnItsTicket(t *testing.T) {
	t.Parallel()
	g := byChartRig(t)
	g.unfold().focus("SHOP-9")
	g.press("s")
	if ref := g.m.selected(); ref == nil || ref.id != "SHOP-9" {
		t.Errorf("by chart the cursor is on %+v, want SHOP-9", ref)
	}
	g.press("s")
	if ref := g.m.selected(); ref == nil || ref.id != "SHOP-9" {
		t.Errorf("by state again the cursor is on %+v, want SHOP-9", ref)
	}
	// A task two issues list: the cursor is under the issue it was on.
	e := g.m.top()
	for i, r := range g.m.mainRefs() {
		if r.id == "SHOP-1" && r.container == "SHOP-50" {
			e.sel, e.follow = i, false
		}
	}
	g.m.noteCursor()
	g.press("s")
	if ref := g.m.selected(); ref == nil || ref.id != "SHOP-1" || ref.container != "SHOP-50" {
		t.Errorf("by chart the cursor is on %+v, want SHOP-1 under SHOP-50", ref)
	}
}
