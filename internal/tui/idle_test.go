package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// draftStatus is `jig status SHOP-1` while the planner drafts: a brief with the
// sections named and slices.yaml with the slices named, none of it approved, and
// no journal line for any of it.
func draftStatus(sections []string, slices ...string) string {
	parts := []string{"ticket: SHOP-1", "state: new"}
	if len(slices) == 0 {
		parts = append(parts, "slices: none")
	} else {
		var rows [][]string
		for _, id := range slices {
			rows = append(rows, []string{id, "queued", "0", "-", "-", "Goal of " + id})
		}
		parts = append(parts, axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows))
	}
	parts = append(parts, "questions: none")
	if len(sections) > 0 {
		var rows [][]string
		for _, h := range sections {
			rows = append(rows, []string{h, "-"})
		}
		parts = append(parts, axi.KV("brief", [][2]string{{"approval", "none"}}), axi.Table("brief_sections", []string{"heading", "slices"}, rows))
	}
	parts = append(parts, axi.Help("Write the brief"))
	return strings.Join(parts, "\n") + "\n"
}

// sections are n headings.
func sections(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("Section %d", i+1)
	}
	return out
}

// quietRig is a screen on SHOP-1 whose clock the test moves: the status was
// read when it opened, and no journal line has come since.
func quietRig(t *testing.T, first string) (g *rig, advance func(time.Duration)) {
	t.Helper()
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	g = newRig(t, "SHOP-1")
	g.m.now = func() time.Time { return now }
	g.r.answer("status SHOP-1 --store /s", first, 0)
	g.feed()
	return g, func(d time.Duration) { now = now.Add(d) }
}

// TestAQuietTicketScreenReadsItsStatusAgainAndSaysWhatAppeared: the planner
// writes brief.md and slices.yaml, which make no journal line, so the sub-steps
// stay "drafted ◐, slices written ○" and the activity "nothing yet" for as long
// as that takes. Once the screen has had no event for three seconds it reads the
// status again, the sub-steps fill in, and the activity says what appeared.
func TestAQuietTicketScreenReadsItsStatusAgainAndSaysWhatAppeared(t *testing.T) {
	t.Parallel()
	g, advance := quietRig(t, draftStatus(nil))
	v := g.view()
	for _, want := range []string{"◐ brief drafted", "○ slices written", "nothing yet"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the screen before the brief is written lacks %q:\n%s", want, v)
		}
	}

	// The brief is written: six sections. No line, no event.
	g.r.answer("status SHOP-1 --store /s", draftStatus(sections(6)), 0)
	advance(2 * time.Second)
	g.send(idleMsg{})
	if n := g.r.statusReads("SHOP-1"); n != 1 {
		t.Fatalf("the status was read %d times after 2s of quiet, want only the first", n)
	}
	advance(1500 * time.Millisecond)
	g.send(idleMsg{})
	if n := g.r.statusReads("SHOP-1"); n != 2 {
		t.Fatalf("the status was read %d times after 3.5s of quiet, want 2", n)
	}
	v = g.view()
	for _, want := range []string{"✓ brief drafted · 6 sections", "◐ slices written", "brief drafted · 6 sections written"} {
		if !strings.Contains(v, want) {
			t.Errorf("after the brief was written the screen lacks %q:\n%s", want, v)
		}
	}
	if act := strings.Join(panelLines(strings.Split(v, "\n"), "Brief activity"), "\n"); strings.Contains(act, "nothing yet") {
		t.Errorf("the activity says \"nothing yet\" while work happened:\n%s", v)
	}

	// slices.yaml is written: two slices.
	g.r.answer("status SHOP-1 --store /s", draftStatus(sections(6), "round", "cart"), 0)
	advance(3 * time.Second)
	g.send(idleMsg{})
	v = g.view()
	for _, want := range []string{"✓ slices written · 2 slices", "slices written · 2 slices written", "brief drafted · 6 sections written"} {
		if !strings.Contains(v, want) {
			t.Errorf("after the slices were written the screen lacks %q:\n%s", want, v)
		}
	}
	// What was there the first time the status was read is not news.
	g2, _ := quietRig(t, draftStatus(sections(3), "round"))
	if v := g2.view(); !strings.Contains(v, "nothing yet") {
		t.Errorf("a brief that was there when the screen opened is an activity line:\n%s", v)
	}
}

// TestTheIdleReadIsRateLimitedAndOnlyForTheWatchedTicket: a read is made at most
// every three seconds, an event of the ticket holds it off for three seconds
// more, and a screen that is not the ticket's (the list) reads nothing.
func TestTheIdleReadIsRateLimitedAndOnlyForTheWatchedTicket(t *testing.T) {
	t.Parallel()
	g, advance := quietRig(t, draftStatus(nil))
	reads := func() int { return g.r.statusReads("SHOP-1") }

	// Looking at the clock often reads once per three seconds.
	for i := 0; i < 9; i++ {
		advance(time.Second)
		g.send(idleMsg{})
	}
	if reads() != 1+3 {
		t.Errorf("9s of quiet read the status %d times, want the first read and 3 more (every 3s)", reads())
	}

	// An event of the ticket is a read of its own, and holds the idle one off.
	before := reads()
	g.feed(jl(at(1, 0), "SHOP-1", "", journal.EventApproval, with("s-planner", "", "", "Dana <dana@example.invalid>", "0123456789ab", "person")))
	if reads() != before+1 {
		t.Fatalf("an event made %d reads, want 1", reads()-before)
	}
	advance(2 * time.Second)
	g.send(idleMsg{})
	if reads() != before+1 {
		t.Errorf("2s after an event the idle read was made")
	}
	advance(1100 * time.Millisecond)
	g.send(idleMsg{})
	if reads() != before+2 {
		t.Errorf("3.1s after an event the idle read was not made")
	}

	// The list reads no ticket's status when it is quiet.
	g.press("esc")
	before = reads()
	advance(time.Minute)
	g.send(idleMsg{})
	if reads() != before {
		t.Errorf("the main screen read the status of a ticket %d times", reads()-before)
	}
}

// TestTheIdleReadWaitsForTheOneUnderWay: a status that is being read is not
// read a second time on top of it, and the screen does not mark the ticket to
// be read again for it.
func TestTheIdleReadWaitsForTheOneUnderWay(t *testing.T) {
	t.Parallel()
	g, advance := quietRig(t, draftStatus(nil))
	tk := g.m.w.tickets["SHOP-1"]
	tk.loading = true
	advance(time.Minute)
	g.send(idleMsg{})
	if g.r.statusReads("SHOP-1") != 1 || tk.dirty {
		t.Errorf("an idle look while a read is out: %d reads, dirty %v, want 1 and false", g.r.statusReads("SHOP-1"), tk.dirty)
	}
}
