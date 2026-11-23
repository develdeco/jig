package tui

import (
	"testing"
	"time"
)

// TestTheScreensReadTheRosterFlag: the list the screens read is the roster, at
// the start and on a later read alike, and the roster's `updated` is the
// ticket's age.
func TestTheScreensReadTheRosterFlag(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	body := `{"tickets":[{"id":"JIG-1","title":"Greeting copy","state":"ready","updated":"2026-10-23T09:05:07Z"},{"id":"JIG-2","title":"Config loader","state":"ready"}]}` + "\n"
	g.r.answer("next --json --roster --store /s", body, 0)
	g.settle(g.m.Init())
	g.settle(g.m.loadRoster())
	rosters := 0
	for _, c := range g.r.calls {
		if c.Args[0] != "next" {
			continue
		}
		if c.Line != "next --json --roster --store /s" {
			t.Errorf("the list was read with %q, want the roster", c.Line)
		}
		rosters++
	}
	if rosters < 2 {
		t.Errorf("%d roster reads, want the start's and a later one", rosters)
	}
	if got, want := g.m.w.tickets["JIG-1"].updated, time.Date(2026, 10, 23, 9, 5, 7, 0, time.UTC); !got.Equal(want) {
		t.Errorf("JIG-1 updated = %s, want %s", got, want)
	}
	if !g.m.w.tickets["JIG-2"].updated.IsZero() {
		t.Errorf("JIG-2 has no journal and says updated %s", g.m.w.tickets["JIG-2"].updated)
	}
}
