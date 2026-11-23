package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/phase"
)

// behindJSON is `jig next checkout --json` once the parent of the published
// JIG-4 merged: the verb reads the stack plan, so JIG-4's publish is current
// again (it owes a merge and a publish) and the run lists it as the first wave.
func behindJSON() string {
	return checkoutJSON(func(v map[string]any) {
		tickets := v["tickets"].([]map[string]any)
		tickets[0] = entry("JIG-4", "Coupon model", "publish", []string{"done", "done", "done", "current"}, true)
		tickets[0]["prompt"] = "Publish JIG-4"
		v["run"] = [][]string{{"JIG-4", "JIG-8"}, {"JIG-6"}}
	})
}

// behindDigest is the digest of the published JIG-4 once its parent merged: its
// publish is current again, ready, and the roster says it is behind.
func behindDigest() *digestInfo {
	d := task("publish", true, "done|approved", "done|3 of 3 green", "done|round 2 clean",
		"current|"+behindText, "pending|after the publish")
	d.Behind = true
	return d
}

// behindChartRig is the chart screen of the fixtures whose JIG-4 is behind, on a
// terminal of 100 by 36. The journal of JIG-4 says it published; its digest, which
// reads its stack plan, says it owes a merge and a publish.
func behindChartRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "")
	lines, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", behindJSON(), status)
	g.pin("JIG-4", behindDigest())
	g.feed(lines...)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 36})
	return onChart(g)
}

// TestAChartRowFollowsTheDigestForATicketThatIsBehind: the digest is
// stack-aware, so a published ticket whose parent merged is at its publish
// again though its journal says it published. The chart's row takes the
// digest's word: it is not done, its publish is the step it stands at, the
// cursor starts on it, and the Detail panel says what it owes and not that it
// is published.
func TestAChartRowFollowsTheDigestForATicketThatIsBehind(t *testing.T) {
	t.Parallel()
	g := behindChartRig(t)
	c := g.m.w.charts["checkout"]
	if own := fakeDigest(g.m, g.m.w.tickets["JIG-4"]).Phases[stepPublish].State; own != phase.StateDone {
		t.Fatalf("the journal's own reading of JIG-4 has its publish %s, want done: the fixture does not reproduce the gap", own)
	}
	row := g.m.chartRows(c)[0]
	if row.id != "JIG-4" || row.done || !row.behind || row.steps[stepPublish].Done || row.state() != StateReady {
		t.Errorf("JIG-4 = done %v behind %v publish done %v state %s, want a row at its publish, ready", row.done, row.behind, row.steps[stepPublish].Done, row.state())
	}
	for i := stepBrief; i < stepPublish; i++ {
		if !row.steps[i].Done {
			t.Errorf("step %s of the behind ticket is not done: the digest says it is", stepNames[i])
		}
	}
	if got := g.m.chartFirstOpen(c); got != 0 {
		t.Errorf("the cursor follows to row %d, want 0, the ticket that is behind", got)
	}

	v := g.view()
	if line := cursorLine(v); !strings.Contains(line, "JIG-4") || !strings.Contains(line, "behind · owes a merge, then a publish") {
		t.Errorf("the cursor is not on JIG-4 with its note, it is on %q:\n%s", line, v)
	}
	if !strings.Contains(v, "0 of 5 published") {
		t.Errorf("the Tickets panel counts the ticket that is behind as published:\n%s", v)
	}
	panel := strings.Join(detailPanelOf(t, v), "\n")
	if !strings.Contains(panel, "state    behind · owes a merge, then a publish") || strings.Contains(panel, "published") {
		t.Errorf("the Detail panel does not say JIG-4 owes a merge and a publish, or says it is published:\n%s", panel)
	}
	golden(t, "chart-behind", v)
}

// cursorLine is the line of a screen the cursor is on, "" when there is none.
func cursorLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "▸") {
			return l
		}
	}
	return ""
}

// TestAChartRowOfATicketThatIsNotBehindKeepsItsOwnWords: a ticket the digest
// has published stays published and the cursor starts on JIG-5.
func TestAChartRowOfATicketThatIsNotBehindKeepsItsOwnWords(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	c := g.m.w.charts["checkout"]
	row := g.m.chartRows(c)[0]
	if !row.done || row.behind || row.state() != StateWaitsToLand {
		t.Errorf("JIG-4 = done %v behind %v state %s, want it waiting to land", row.done, row.behind, row.state())
	}
	if got := g.m.chartFirstOpen(c); got != 1 {
		t.Errorf("the cursor follows to row %d, want 1, JIG-5", got)
	}
	g.press("up")
	if panel := strings.Join(detailPanelOf(t, g.view()), "\n"); !strings.Contains(panel, "state    waits to land") || strings.Contains(panel, "behind") {
		t.Errorf("the Detail panel of a published ticket does not say so:\n%s", panel)
	}
}
