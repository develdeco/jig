package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// TestATrysRunsAndASlicesTriesReadNewestFirst: a slice's commits are listed
// newest first by the line that began each, a commit run again keeps its place
// and shows its runs newest first, and of two commits begun in one instant the
// later written is above. A verify-pending line begins none.
func TestATrysRunsAndASlicesTriesReadNewestFirst(t *testing.T) {
	t.Parallel()
	run := func(min, sec int, commit byte, outcome, command string) events.Envelope {
		return jl(at(min, sec), "JIG-1", "a", journal.EventOracle, with("", sha(commit), outcome, "", "", ""), func(l *journal.Line) { l.Command, l.Seconds = command, 3 })
	}
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "3", "-", "-", "Greet", "app", "unit", "-"}), 0)
	g.feed(
		run(0, 30, 'a', "fail", "first run"),
		run(1, 30, 'b', "fail", "only run"), run(1, 30, 'c', "fail", "third run"), // one instant: c was written after b
		run(3, 0, 'a', "fail", "second run"), // the first commit is run again, after the others began
	).press("enter").sized(100, 60)
	text := pageText(g)
	order := []string{"ccccccc", "bbbbbbb", "aaaaaaa", "`second run`", "`first run`"}
	last := -1
	for _, want := range order {
		at := strings.Index(text, want)
		if at < 0 || at < last {
			t.Fatalf("%q is not below what precedes it (at %d after %d):\n%s", want, at, last, text)
		}
		last = at
	}
	if strings.Contains(text, "earlier commit") {
		t.Errorf("three commits are counted as cut:\n%s", text)
	}
}

// TestTheSlicesTimelineReadsNewestFirstAndCountsTheOlder: the slice screen's
// timeline keeps its latest forty lines, newest on top, and ends with the count
// of the ones left out; its verification block lists the newest commit first.
func TestTheSlicesTimelineReadsNewestFirstAndCountsTheOlder(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"})+axi.Table("verification_states", []string{"slice", "commit", "state", "clear"}, [][]string{
		{"a", sha('a')[:12], "no recorded result", "jig verify JIG-1 --slice a"},
		{"a", sha('b')[:12], "no recorded result", "jig verify JIG-1 --slice a"},
	}), 0)
	var lines []events.Envelope
	for i := 0; i < 45; i++ {
		lines = append(lines, jl(at(i, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	}
	lines = append(lines,
		jl(at(50, 0), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "first", "", "")),
		jl(at(51, 0), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('b'), "", "second", "", "")))
	g.feed(backwards(lines)...).press("enter", "enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want slice", g.m.Screen())
	}
	text := pageText(g)
	block, timeline, _ := strings.Cut(text, "Timeline\n")
	if b, a := strings.Index(block, "bbbbbbb"), strings.Index(block, "aaaaaaa"); b < 0 || a < b {
		t.Errorf("the verification block does not list the newest commit first (b at %d, a at %d):\n%s", b, a, block)
	}
	rows := strings.Split(strings.TrimRight(timeline, "\n"), "\n")
	rows = rows[1:] // the names of the columns are no row of the list
	if len(rows) != timelineLines+1 {
		t.Fatalf("%d rows in the timeline, want %d and the older row:\n%s", len(rows), timelineLines, timeline)
	}
	newestOnTop(t, "timeline", clocksOf(rows), timelineLines)
	if got := clocksOf(rows)[0]; got != "09:51:00" {
		t.Errorf("the first row is %s, want 09:51:00", got)
	}
	if got, want := rows[len(rows)-1], "↓ 7 older"; strings.TrimSpace(got) != want {
		t.Errorf("the last row is %q, want %q", got, want)
	}
}
