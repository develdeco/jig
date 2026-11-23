package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// builtRig is SHOP-1 with slice round built and verified: claimed at 09:00:00 by
// s-b, which then ran one command every gap until 10:15:00, and verified at
// 10:15:10, 4510 seconds in all.
func builtRig(t *testing.T, calls int, gap time.Duration) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T11:00:00Z")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	start := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	envs := []events.Envelope{jl(start.Format(time.RFC3339), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", ""))}
	for i := 1; i <= calls; i++ {
		envs = append(envs, toolOn("SHOP-1", start.Add(time.Duration(i)*gap).Format(time.RFC3339), "s-b", "go test ./...", said("Run the tests")))
	}
	envs = append(envs, jl("2026-10-23T10:15:10Z", "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")))
	// The stream brings them in batches, as polls do.
	for i := 0; i < len(envs); i += 100 {
		g.feed(envs[i:min(len(envs), i+100)]...)
	}
	return g
}

// TestADoneStepReadsTheSameWhateverTheTailKept: a build whose calls outran the
// ledger's tail reads the time it took, whole, and the same as one that kept
// every call: a call that left the tail keeps the stretch of work it bounded.
func TestADoneStepReadsTheSameWhateverTheTailKept(t *testing.T) {
	t.Parallel()
	many := builtRig(t, 450, 10*time.Second) // 50 calls more than the tail holds
	few := builtRig(t, 30, 150*time.Second)  // all of them in the tail
	tk := many.m.w.tickets["SHOP-1"]
	if len(tk.ledger) != ledgerCap || len(tk.older) == 0 || len(tk.older) > ledgerCap/4 {
		t.Fatalf("the ticket kept %d calls in its tail and folded %d, want a full tail and a few folded", len(tk.ledger), len(tk.older))
	}
	if len(few.m.w.tickets["SHOP-1"].older) != 0 {
		t.Fatalf("the ticket of 30 calls folded some")
	}
	want := "1h 15m"
	for name, g := range map[string]*rig{"450 calls": many, "30 calls": few} {
		if got := g.m.steps(g.m.w.tickets["SHOP-1"])[stepBuild].Took; got != want {
			t.Errorf("the build of %s took %q, want %q", name, got, want)
		}
		tk := g.m.w.tickets["SHOP-1"]
		subs := g.m.phaseOf(tk).Of(phase.Build).Subs
		if got := subSummaryOf(g, stepBuild, subs[0]); got != "took "+want {
			t.Errorf("its slice of %s reads %q, want took %s", name, got, want)
		}
	}
}

// subSummaryOf is what the sub-steps block says of a done sub-step's time.
func subSummaryOf(g *rig, step int, s phase.Sub) string {
	tk := g.m.w.tickets["SHOP-1"]
	return g.m.subSummary(g.m.clocks(tk, g.m.phaseOf(tk)), step, s)
}

// TestADoneStepReadsTheSameWhenOpenedAfterIt: a ticket whose screen watched the
// brief written, and one opened after it was, read the brief alike: what the
// screen wrote when it noticed a change says when it looked, not when the work
// was done.
func TestADoneStepReadsTheSameWhenOpenedAfterIt(t *testing.T) {
	t.Parallel()
	watched, advance := quietRig(t, draftStatus(nil))
	watched.feed(
		toolOn("SHOP-1", at(28, 1), "s-b", "jig ticket new --title Round", said("Mint the ticket")),
		toolOn("SHOP-1", at(28, 4), "s-b", "jig brief show SHOP-1", said("Read the brief")),
		jl(at(28, 6), "SHOP-1", "", journal.EventRevalidate, with("s-b", "", "", "", "", "")),
		toolOn("SHOP-1", at(28, 7), "s-b", "jig validate SHOP-1", said("Check the slices")),
	)
	watched.r.answer("status SHOP-1 --store /s", draftStatus(sections(3), "round"), 0)
	advance(4 * time.Second)
	watched.send(idleMsg{})
	if n := len(watched.m.w.tickets["SHOP-1"].seen); n == 0 {
		t.Fatalf("the screen that watched noted nothing of the brief")
	}

	after := newRig(t, "SHOP-1")
	atClock(after, "2026-10-23T09:30:04Z")
	after.r.answer("status SHOP-1 --store /s", draftStatus(sections(3), "round"), 0)
	after.feed(
		toolOn("SHOP-1", at(28, 1), "s-b", "jig ticket new --title Round", said("Mint the ticket")),
		toolOn("SHOP-1", at(28, 4), "s-b", "jig brief show SHOP-1", said("Read the brief")),
		jl(at(28, 6), "SHOP-1", "", journal.EventRevalidate, with("s-b", "", "", "", "", "")),
		toolOn("SHOP-1", at(28, 7), "s-b", "jig validate SHOP-1", said("Check the slices")),
	)
	for name, g := range map[string]*rig{"watched": watched, "opened after": after} {
		if got := g.m.steps(g.m.w.tickets["SHOP-1"])[stepBrief].Took; got != "6s" {
			t.Errorf("the brief of the screen that %s took %q, want 6s", name, got)
		}
	}
}

// TestWhatLeavesTheTailKeepsWhatTheWorkNeeds: of the lines that leave the
// ledger's tail the work keeps the session that tied a holder to the ticket and
// a call that no later call bounds, and lets go of the words of the rest.
func TestWhatLeavesTheTailKeepsWhatTheWorkNeeds(t *testing.T) {
	t.Parallel()
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T11:00:00Z")
	envs := []events.Envelope{ll(at(0, 0), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-p" })}
	for i := 1; i <= ledgerCap+10; i++ {
		envs = append(envs, toolOn("SHOP-1", time.Date(2026, 10, 23, 9, 0, i, 0, time.UTC).Format(time.RFC3339), "s-p", "go test ./...", said("Run the tests")))
	}
	for i := 0; i < len(envs); i += 100 {
		g.feed(envs[i:min(len(envs), i+100)]...)
	}
	tk := g.m.w.tickets["SHOP-1"]
	if len(tk.older) == 0 || tk.older[0].Event != journal.EventSessionTouch || tk.older[0].Session != "s-p" {
		t.Fatalf("the ticket folded %+v, want the session line that tied s-p to it first", tk.older)
	}
	for _, l := range tk.older {
		if l.Description != "" || (isMove(l) && l.Command != "…") {
			t.Errorf("a folded line kept its words: %+v", l.Line)
		}
	}
	sub := phase.Sub{Key: "drafted", State: phase.SubActive}
	if tr := tk.openTrack(stepBrief, sub); tr.holder != "s-p" || !tr.start.Equal(time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("the holder of a sub-step with no line of its own is %q since %v, want s-p since the session line that left the tail", tr.holder, tr.start)
	}

	// A call with no call after it to bound it is kept.
	lone := &ticket{}
	lone.fold([]events.Envelope{toolOn("SHOP-1", at(1, 0), "s-p", "ls"), toolOn("SHOP-1", at(1, 5), "s-p", "ls")},
		[]events.Envelope{ll(at(1, 9), "SHOP-1", journal.EventHook)})
	if len(lone.older) != 2 {
		t.Errorf("the calls before nothing were folded to %d, want both kept", len(lone.older))
	}
}

// TestOneCounterReadsTheSameInBothPlaces: a step's time on its row is the sum
// of the work its sub-steps and the step itself counted, and the time a done
// sub-step reads in the block is the one the sum took.
func TestOneCounterReadsTheSameInBothPlaces(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"}, {"b", "green", "1", "-", "-", "Do b"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T09:30:00Z")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.feed(
		jl(at(0, 0), "SHOP-1", "a", journal.EventClaim, with("s-b", "", "", "", "", "")),
		toolOn("SHOP-1", at(0, 10), "s-b", "go test ./...", said("Run the tests")),
		jl(at(0, 40), "SHOP-1", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(0, 40), "SHOP-1", "b", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(1, 10), "SHOP-1", "b", journal.EventVerified, with("", sha('b'), "", "", "", "")),
	)
	tk := g.m.w.tickets["SHOP-1"]
	d := g.m.phaseOf(tk)
	c := g.m.clocks(tk, d)
	var sum time.Duration
	for _, s := range d.Of(phase.Build).Subs {
		sum += c.work.of(stepBuild, s.Key)
	}
	sum += c.work.of(stepBuild, "")
	row := g.m.steps(tk)[stepBuild]
	if row.took != sum || row.took != c.work.ofStep(stepBuild) || row.Took != "1m 10s" {
		t.Errorf("the row says %v (%q), its sub-steps and the step's own add up to %v, want 1m 10s in both", row.took, row.Took, sum)
	}
	panel := g.m.substepsPanel(tk, stepBuild, d.Of(phase.Build).Subs, 10)
	var lines []string
	for _, l := range panel.lines {
		lines = append(lines, l.text)
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"slice a  verified aaaaaaa · took 40s", "slice b  verified bbbbbbb · took 30s"} {
		if !strings.Contains(text, want) {
			t.Errorf("the block lacks %q:\n%s", want, text)
		}
	}
}
