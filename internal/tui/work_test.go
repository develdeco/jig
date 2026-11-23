package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// draftedRig is SHOP-1 whose brief was drafted in six seconds by session s-b
// (two calls, then the slices check that wrote its line) and has stood waiting
// for the person's approval since: the drafting started at 09:00:01 and the
// screen reads the ticket an hour after.
func draftedRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "SHOP-1")
	g.m.now = func() time.Time { return time.Date(2026, 10, 23, 10, 0, 7, 0, time.UTC) }
	g.r.answer("status SHOP-1 --store /s", draftStatus(sections(3), "round"), 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(
		toolOn("SHOP-1", at(0, 1), "s-b", "jig ticket new --title Round", said("Mint the ticket")),
		toolOn("SHOP-1", at(0, 4), "s-b", "jig brief show SHOP-1", said("Read the brief")),
		jl(at(0, 6), "SHOP-1", "", journal.EventRevalidate, with("s-b", "", "", "", "", "")),
		toolOn("SHOP-1", at(0, 7), "s-b", "jig validate SHOP-1", said("Check the slices")),
	)
	return g
}

// briefWork is what the Steps panel says of the brief: its row and the panel's
// title.
func briefWork(g *rig) (row, title string) {
	g.t.Helper()
	panel := panelLines(strings.Split(g.view(), "\n"), "Steps")
	if len(panel) < 2 {
		g.t.Fatalf("no Steps panel:\n%s", g.view())
	}
	for _, r := range panel {
		if strings.Contains(r, "Brief") {
			row = r
		}
	}
	return row, panel[0]
}

// TestADraftedBriefWaitingAnHourReadsItsDraftingTime: the brief took six
// seconds to draft and has waited an hour for its approval; the wait is not
// work, so the row and the total read the six seconds, whatever the time.
func TestADraftedBriefWaitingAnHourReadsItsDraftingTime(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	if got := g.m.steps(g.m.w.tickets["SHOP-1"])[stepBrief].Took; got != "6s" {
		t.Errorf("the brief took %q, want the 6s of its drafting", got)
	}
	row, title := briefWork(g)
	if !strings.Contains(row, "6s") || !strings.Contains(title, "worked 6s") {
		t.Errorf("an hour after the draft the brief reads\n%s\n%s\nwant 6s in the row and in the total", title, row)
	}
}

// TestACallOfTheDraftingSessionAfterTheDraftIsNoBriefWork: the session that
// drafted the brief goes on to other work, on this ticket and on another one;
// no call of it after the draft counts for the brief, and a call of this
// ticket that no sub-step held is placed in no step.
func TestACallOfTheDraftingSessionAfterTheDraftIsNoBriefWork(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	g.feed(
		toolOn("SHOP-1", at(30, 0), "s-b", "go test ./...", said("Run the tests")),
		toolOn("SHOP-2", at(31, 0), "s-b", "jig ticket new --title Other", said("Mint the other ticket")),
	)
	tk := g.m.w.tickets["SHOP-1"]
	if got := g.m.steps(tk)[stepBrief].Took; got != "6s" {
		t.Errorf("the brief took %q after the session's later calls, want 6s", got)
	}
	if row, title := briefWork(g); !strings.Contains(row, "6s") || !strings.Contains(title, "worked 6s") {
		t.Errorf("the screen after the later calls reads\n%s\n%s\nwant 6s in the row and in the total", title, row)
	}
	late := 0
	for _, p := range g.m.placed(tk, g.m.phaseOf(tk)) {
		if p.l.Command == "go test ./..." {
			late++
			if p.step != -1 {
				t.Errorf("the later call is placed in step %d, want none", p.step)
			}
		}
	}
	if late != 1 {
		t.Errorf("the log holds %d later calls, want 1", late)
	}
}

// TestACallWhileASliceWaitsForAnAnswerIsNoWork: the builder asked a question on
// its slice and went on with a command of its own; the slice waits for the
// person, nobody holds it, and the command is placed in no sub-step.
func TestACallWhileASliceWaitsForAnAnswerIsNoWork(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"c", "needs-input", "1", "-", "q-001", "Pick the tone"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
	}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.m.now = func() time.Time { return time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC) }
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.feed(
		jl(at(1, 0), "SHOP-1", "c", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(2, 0), "SHOP-1", "c", journal.EventQuestion, with("s-b", "", "", "Formal or casual?", "q-001", "agent")),
		toolOn("SHOP-1", at(5, 0), "s-b", "go test ./...", said("Run the tests")),
	)
	tk := g.m.w.tickets["SHOP-1"]
	for _, p := range g.m.placed(tk, g.m.phaseOf(tk)) {
		if p.l.Command != "" && p.step != -1 {
			t.Errorf("the call made while the slice waits is placed in %s, want no step", tagOf(p.step, p.key))
		}
	}
	if got := g.m.steps(tk)[stepBuild].Took; got != "1m 00s" {
		t.Errorf("the build took %q, want the minute between its claim and its question", got)
	}
}

// TestNobodyHoldsASubStepThatWaitsForAPerson: the session that wrote the
// ticket's latest line is no holder of the brief's approval, which waits for
// the person.
func TestNobodyHoldsASubStepThatWaitsForAPerson(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	tk := g.m.w.tickets["SHOP-1"]
	var approved *phase.Sub
	subs := g.m.phaseOf(tk).Of(phase.Brief).Subs
	for i := range subs {
		if subs[i].Key == "approved" {
			approved = &subs[i]
		}
	}
	if approved == nil || approved.State != phase.SubNeedsYou {
		t.Fatalf("the brief's approval = %+v, want it waiting for the person", approved)
	}
	if tr := tk.openTrack(stepBrief, *approved); tr.holder != "" {
		t.Errorf("the approval is held by %q, want nobody", tr.holder)
	}
}
