package tui

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestAMessageBuildsNoPageAndAFrameBuildsOne: holding the reader across new
// lines costs the frame nothing extra and a message nothing at all.
func TestAMessageBuildsNoPageAndAFrameBuildsOne(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"}), 0)
	g.feed(jl(at(0, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	g.view()
	before := g.m.pageBuilds
	g.m.Update(eventsMsg{jl(at(1, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))})
	if got := g.m.pageBuilds - before; got != 0 {
		t.Errorf("a message built %d pages, want 0", got)
	}
	g.view()
	if got := g.m.pageBuilds - before; got != 1 {
		t.Errorf("a message and its frame built %d pages, want 1", got)
	}
}

// TestTheCountOfNewRowsIsRowsAndNotLines: a record of two lines that comes in
// above the window is one row.
func TestTheCountOfNewRowsIsRowsAndNotLines(t *testing.T) {
	t.Parallel()
	h := &held{lines: []string{"rounds", "round 1  findings", "    reviewer", "asks"}, start: 1, rows: 3}
	pg := page{}
	for _, l := range []string{"rounds", "round 2  open", "    waiting for a reviewer", "round 1  findings", "    reviewer", "asks"} {
		pg.add(l)
	}
	shift, rows := lineShift(h, pg)
	if shift != 2 || rows != 1 {
		t.Errorf("shift %d, rows %d; want 2 lines and 1 row", shift, rows)
	}
}
