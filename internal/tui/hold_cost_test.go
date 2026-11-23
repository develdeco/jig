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

// TestTheCountOfNewRowsIsRowsThatCameInAndNotLines: the count is of the rows
// that came in above the window wherever above it they came, a record's later
// lines going on its row, rows of a nested list each counting, and a line above
// that went away counting nothing.
func TestTheCountOfNewRowsIsRowsThatCameInAndNotLines(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		old, now    []string
		start       int
		shift, rows int
	}{
		"a record just above": {
			[]string{"rounds", "round 1  findings", "    reviewer", "asks"},
			[]string{"rounds", "round 2  open", "    waiting for a reviewer", "round 1  findings", "    reviewer", "asks"},
			1, 2, 1,
		},
		"a record further up, over lines the reader had": {
			[]string{"rounds", "round 1  findings", "probes", "as recorded", "p1", "p2", "asks"},
			[]string{"rounds", "round 2  open", "    waiting for a reviewer", "round 1  findings", "probes", "as recorded", "p1", "p2", "asks"},
			4, 2, 1,
		},
		"two tries in a nested list": {
			[]string{"a  slice", "       try 1", "         run 1", "sessions"},
			[]string{"a  slice", "       try 3", "         run 3", "       try 2", "         run 2", "       try 1", "         run 1", "sessions"},
			3, 4, 2,
		},
		"a blank line is no row": {
			[]string{"one", "two"},
			[]string{"one", "", "two"},
			1, 1, 0,
		},
		"a line above that went away": {
			[]string{"one", "two", "three", "four"},
			[]string{"one", "three", "four"},
			3, 0, 0,
		},
	} {
		h := &held{start: tc.start, rows: 3}
		h.lines = tc.old
		pg := page{}
		for _, l := range tc.now {
			pg.add(l)
		}
		if shift, rows := lineShift(h, pg); shift != tc.shift || rows != tc.rows {
			t.Errorf("%s: shift %d, rows %d; want %d lines and %d rows", name, shift, rows, tc.shift, tc.rows)
		}
	}
}
