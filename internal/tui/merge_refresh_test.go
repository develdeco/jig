package tui

import (
	"strings"
	"testing"
)

const (
	refreshVerb = "next --refresh-merges --store /s"
	rosterVerb  = "next --json --roster --store /s"
)

// nextLines are the reads of the list the rig ran after the call numbered from,
// in order.
func nextLines(g *rig, from int) []string {
	var out []string
	for _, c := range g.r.calls[from:] {
		if c.Args[0] == "next" {
			out = append(out, c.Line)
		}
	}
	return out
}

// TestMainsMergeTimerRefreshesKeptStatesThenTheRoster: on the project's list,
// when a row waits on a merge, the merge timer reads the refresh, which
// asks the host for the kept states that are old, and then the roster; a list
// with no such row reads nothing on the timer.
func TestMainsMergeTimerRefreshesKeptStatesThenTheRoster(t *testing.T) {
	t.Parallel()
	g := newRig(t, "").feed(jig2()...)
	if g.m.Screen() != "main" || !g.m.mainWaitsOnMerge() {
		t.Fatalf("on %s, waits on a merge %v: want main with a row that waits", g.m.Screen(), g.m.mainWaitsOnMerge())
	}
	from := len(g.r.calls)
	g.send(mergeMsg{})
	if got := strings.Join(nextLines(g, from), " | "); got != refreshVerb+" | "+rosterVerb {
		t.Errorf("the timer ran %q, want the refresh and then the roster", got)
	}

	q := newQuietRig(t, "").feed(jig1()...)
	if q.m.mainWaitsOnMerge() {
		t.Fatalf("a ticket in its build waits on a merge")
	}
	from = len(q.r.calls)
	q.send(mergeMsg{})
	if got := nextLines(q, from); len(got) != 0 {
		t.Errorf("a list with no row that waits on a merge read %v on the timer", got)
	}
}

// TestROnMainRefreshesKeptMergeStates: r on the project's list reads the
// refresh in the background and then the roster.
func TestROnMainRefreshesKeptMergeStates(t *testing.T) {
	t.Parallel()
	g := newRig(t, "").feed(jig1()...)
	from := len(g.r.calls)
	g.press("r")
	if got := strings.Join(nextLines(g, from), " | "); got != refreshVerb+" | "+rosterVerb {
		t.Errorf("r ran %q, want the refresh and then the roster", got)
	}
}
