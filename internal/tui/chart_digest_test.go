package tui

import (
	"strings"
	"testing"
)

// TestChartRowAndDetailAgreeWithTheDigest: a chart's row and its Detail panel
// say what the ticket's digest says, so a ticket that waits for a merge reads
// waits for on its row and in Detail, never ready to build, and a published
// ticket reads the same in the row, the Detail and its own screen.
func TestChartRowAndDetailAgreeWithTheDigest(t *testing.T) {
	t.Parallel()
	pending := []string{"pending|after the build", "pending|after a clean review", "pending|after the publish"}
	g := chartRig(t)
	// JIG-7 stands at its build and cannot start: its digest says so, and the
	// roster lists the merge it waits for.
	g.pin("JIG-7", task("build", false, "done|approved by Ada", "current|waits for JIG-4 to merge", pending[0], pending[1], pending[2]), "blocked")
	onChart(g)
	g.press("down", "down", "down", "down")
	view := g.view()
	row := lineWith(view, "JIG-7")
	if !strings.Contains(row, "waits for JIG-4 merged") || strings.Contains(row, "ready") {
		t.Errorf("JIG-7's row does not say it waits for a merge:\n%s", row)
	}
	panel := strings.Join(detailPanelOf(t, view), "\n")
	if strings.Contains(panel, "ready to build") || !strings.Contains(panel, "waiting to build") {
		t.Errorf("JIG-7's Detail says it can start, or does not say it waits:\n%s", panel)
	}

	// A published ticket reads published on its row, in the Detail and on its
	// own screen, from one digest.
	g.press("up", "up", "up", "up")
	view = g.view()
	if row := lineWith(view, "JIG-4"); !strings.Contains(row, "✓ ✓ ✓ ✓") || !strings.Contains(row, "published") {
		t.Errorf("JIG-4's row does not say it published:\n%s", row)
	}
	if panel := strings.Join(detailPanelOf(t, view), "\n"); !strings.Contains(panel, "state    published") {
		t.Errorf("JIG-4's Detail does not say it published:\n%s", panel)
	}
	g.press("enter")
	if g.m.Screen() != "ticket" {
		t.Fatalf("on %s, want the ticket screen", g.m.Screen())
	}
	if screen := g.view(); !strings.Contains(screen, "✓ published") || !strings.Contains(screen, "4 Publish") {
		t.Errorf("JIG-4's screen does not say it published:\n%s", screen)
	}
}
