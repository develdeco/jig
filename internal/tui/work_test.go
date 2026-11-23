package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
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
		jl(at(0, 7), "SHOP-1", "", journal.EventRevalidate, with("s-b", "", "", "", "", "")),
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
	if !strings.Contains(row, "6s") || !strings.Contains(title, "total 6s") {
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
	if row, title := briefWork(g); !strings.Contains(row, "6s") || !strings.Contains(title, "total 6s") {
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
