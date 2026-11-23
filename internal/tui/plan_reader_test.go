package tui

import (
	"strings"
	"testing"
)

// TestAnIssuesPlanIsDrawnBeforeItsChartIsRead: the store listing names the issue
// each chart plans, so the screen of such a chart is the plan from its first
// frame, saying it is reading, and never the legacy chart screen first.
func TestAnIssuesPlanIsDrawnBeforeItsChartIsRead(t *testing.T) {
	t.Parallel()
	g := planMain(t, planSpec{})
	g.m.height, g.m.width = 40, 100
	g.m.w.charts["repeat"].info = nil
	g.m.nav = append(g.m.nav, navEntry{screen: screenChart, chart: "repeat", follow: true})
	v := stripANSI(g.view())
	for _, want := range []string{"Need", "Tasks", "Issue", readingText} {
		if !strings.Contains(v, want) {
			t.Errorf("the plan before its chart is read lacks %q:\n%s", want, v)
		}
	}
	for _, bad := range []string{"Tickets", "Run", "Map"} {
		if strings.Contains(v, bad) {
			t.Errorf("the screen before its chart is read is the legacy chart's (%q):\n%s", bad, v)
		}
	}
	if !strings.Contains(v, "m map") && !strings.Contains(v, "enter open") {
		t.Errorf("the hints are missing:\n%s", v)
	}
}

// TestAMultiTaskWaitsForAnUngraduatedEntryAsItsStatusSays: the multi-task's
// pull-request line takes its waits from the status that derives them, so an
// entry under it with no ticket yet is waited for and no prompt to publish shows.
func TestAMultiTaskWaitsForAnUngraduatedEntryAsItsStatusSays(t *testing.T) {
	t.Parallel()
	kids := &digestInfo{Kind: "multi-task", Parent: "SHOP-49", State: "waiting", Children: []digestChild{{ID: "SHOP-5"}, {ID: "SHOP-6"}}}
	g := planRig(t, planSpec{multi: true, kidsLanded: true, multiDigest: kids})
	g.r.answer("status SHOP-7 --store /s", "ticket: SHOP-7\nstate: multi-task\nkind: multi-task\nbranch: 503-the-multi-task\nstanding: waiting\nprompt: \"\"\nwaits[1]{ticket,standing}:\n  \"3 Entry\",not graduated\nneed: x\n", 0)
	g.m.height, g.m.width = 40, 100
	g.m.w.tickets["SHOP-7"].dirty = true
	onRow(t, g, "SHOP-7")
	g.press("enter")
	row := issueLine(stripANSI(g.view()), "PR")
	if !strings.Contains(row, "waits for 1 task") || strings.Contains(row, "Publish") {
		t.Errorf("the multi-task's pull request line: %q", row)
	}
}

// TestPlanPRWaitsForAnUngraduatedEntry: an entry of the chart with no ticket yet
// is waited for by the issue's pull request, which never reads ready to open
// before `jig next` says the issue is ready.
func TestPlanPRWaitsForAnUngraduatedEntry(t *testing.T) {
	t.Parallel()
	ready := &digestInfo{Kind: "issue", State: "waiting"}
	g := planRig(t, planSpec{issue: ready, prompt: "Publish SHOP-49", waits: []map[string]any{{"ticket": "", "standing": "not graduated"}}})
	g.m.height, g.m.width = 40, 100
	row := issueLine(stripANSI(g.view()), "PR")
	if !strings.Contains(row, "not opened yet · waits for 1 task") || strings.Contains(row, "ready to open") || strings.Contains(row, "Publish") {
		t.Errorf("the pull request line with an entry not graduated: %q", row)
	}
	done := planRig(t, planSpec{issue: ready, prompt: "Publish SHOP-49"})
	done.m.height, done.m.width = 40, 100
	if row := issueLine(stripANSI(done.view()), "PR"); !strings.Contains(row, `say "Publish SHOP-49"`) {
		t.Errorf("with nothing left to wait for: %q", row)
	}
}
