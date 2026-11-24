package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// buildingRig is SHOP-1 with slice round being built by session s-b: it
// claimed the slice at 09:00:00 and ran two commands, the last at 09:02:00.
func atWorkRig(t *testing.T) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "building", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T09:02:30Z")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(
		jl(at(0, 0), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		toolOn("SHOP-1", at(1, 0), "s-b", "go test ./...", said("Run the tests")),
		toolOn("SHOP-1", at(2, 0), "s-b", "git status", said("Look at the change")),
	)
	return g
}

// TestAStepWhoseHolderEndedStopsAndSaysIdleSince: the session building the
// slice ended at 09:03; the step counts its work to its last call and says since
// when nobody has worked on it, in the screen's zone, though the window of
// silence is not over; the time after is in no counter.
func TestAStepWhoseHolderEndedStopsAndSaysIdleSince(t *testing.T) {
	t.Parallel()
	g := atWorkRig(t)
	if row := stepsRow(g, stepBuild); strings.Contains(row, "idle") || !strings.Contains(row, "2m 30s") {
		t.Errorf("with its builder at work the build reads\n%s\nwant 2m 30s and no idle", row)
	}
	atClock(g, "2026-10-23T09:03:30Z")
	g.feed(ll("2026-10-23T09:03:00Z", "", journal.EventSessionEnd, func(l *journal.Line) { l.Session = "s-b" }))
	atClock(g, "2026-10-23T09:04:00Z")
	row := stepsRow(g, stepBuild)
	if !strings.Contains(row, " 2m 00s ") || !strings.Contains(row, "idle since 09:02") {
		t.Errorf("after its builder's session ended the build reads\n%s\nwant 2m 00s and idle since 09:02", row)
	}
	atClock(g, "2026-10-23T09:14:00Z")
	if got := g.m.steps(g.m.w.tickets["SHOP-1"])[stepBuild].Took; got != "2m 00s" {
		t.Errorf("ten minutes on the build took %q, want its idle time in no counter", got)
	}

	// The zone is the screen's, not the stream's.
	g.m.zone = time.FixedZone("east", 3*3600)
	if row := stepsRow(g, stepBuild); !strings.Contains(row, "idle since 12:02") {
		t.Errorf("in a zone 3 hours east the build reads\n%s\nwant idle since 12:02", row)
	}
}

// TestASubagentStopEndsItsHolding: a subagent that stopped after its last call
// is gone; one that stopped before it, and went on, is not.
func TestASubagentStopEndsItsHolding(t *testing.T) {
	t.Parallel()
	g := atWorkRig(t)
	tk := g.m.w.tickets["SHOP-1"]
	w := moveWindow{holder: "s-b", agent: "rev"}
	last := time.Date(2026, 10, 23, 9, 2, 0, 0, time.UTC)
	stop := func(ts, agent, summary string) events.Envelope {
		return ll(ts, "SHOP-1", journal.EventHook, func(l *journal.Line) {
			l.Session, l.Agent, l.Outcome, l.Summary = "s-b", agent, "stop", summary
		})
	}
	call := func(ts string) events.Envelope {
		return toolOn("SHOP-1", ts, "s-b", "go vet ./...", func(l *journal.Line) { l.Agent = "rev" })
	}
	tk.ledger = append(tk.ledger, call("2026-10-23T09:02:10Z"))
	if _, idle := g.m.idleOf(tk, w, last); idle {
		t.Errorf("a subagent at work is idle")
	}
	tk.ledger = append(tk.ledger, stop("2026-10-23T09:02:15Z", "other", "nothing outstanding"), stop("2026-10-23T09:02:16Z", "rev", "blocked: a slice is open"))
	if _, idle := g.m.idleOf(tk, w, last); idle {
		t.Errorf("a subagent that was held back from stopping, and another one's stop, are taken for its stop")
	}
	tk.ledger = append(tk.ledger, stop("2026-10-23T09:02:20Z", "rev", "nothing outstanding"))
	if at, idle := g.m.idleOf(tk, w, last); !idle || !at.Equal(time.Date(2026, 10, 23, 9, 2, 10, 0, time.UTC)) {
		t.Errorf("a subagent that stopped after its last call: idle %v since %v, want idle since its last call", idle, at)
	}
	tk.ledger = append(tk.ledger, toolOn("SHOP-1", "2026-10-23T09:02:22Z", "s-b", "go vet ./...", func(l *journal.Line) { l.Agent = "other" }))
	if _, idle := g.m.idleOf(tk, w, last); !idle {
		t.Errorf("a call of another subagent of the session brought the stopped one back")
	}
	tk.ledger = append(tk.ledger, call("2026-10-23T09:02:25Z"))
	if _, idle := g.m.idleOf(tk, w, last); idle {
		t.Errorf("a subagent that called again after its stop is idle")
	}
}

// TestAStepWhoseHolderIsQuietPastTheWindowStops: a builder that has made no
// call for longer than the window reads idle since its last; one within the
// window is still at work.
func TestAStepWhoseHolderIsQuietPastTheWindowStops(t *testing.T) {
	t.Parallel()
	g := atWorkRig(t)
	atClock(g, "2026-10-23T09:06:50Z") // 4m 50s after the last call
	if row := stepsRow(g, stepBuild); strings.Contains(row, "idle") || !strings.Contains(row, "6m 50s") {
		t.Errorf("a builder silent for under the window: the build reads\n%s\nwant it counting on", row)
	}
	atClock(g, "2026-10-23T09:07:01Z") // 5m 01s
	row := stepsRow(g, stepBuild)
	if !strings.Contains(row, " 2m 00s ") || !strings.Contains(row, "idle since 09:02") {
		t.Errorf("a builder silent past the window: the build reads\n%s\nwant 2m 00s and idle since 09:02", row)
	}
	g.m.st = styles{r: colored()}
	if view := g.view(); !strings.Contains(view, g.m.st.dim("0 of 1 green · slice round is being built")) || !strings.Contains(view, g.m.st.dim("· idle since 09:02")) {
		t.Errorf("the idle words are not in the dim colour:\n%q", view)
	}
}

// TestAStepLiveAgainCountsOnFromItsNextCall: the builder comes back after 20
// minutes of silence; the step counts from that call on, and the silence stays
// out of every counter.
func TestAStepLiveAgainCountsOnFromItsNextCall(t *testing.T) {
	t.Parallel()
	g := atWorkRig(t)
	atClock(g, "2026-10-23T09:22:00Z")
	if got := g.m.steps(g.m.w.tickets["SHOP-1"])[stepBuild].Took; got != "2m 00s" {
		t.Fatalf("the build after 20 silent minutes took %q, want 2m 00s", got)
	}
	g.feed(toolOn("SHOP-1", "2026-10-23T09:22:00Z", "s-b", "go test ./...", said("Run the tests")))
	atClock(g, "2026-10-23T09:22:40Z")
	row := stepsRow(g, stepBuild)
	if !strings.Contains(row, " 2m 40s ") || strings.Contains(row, "idle") {
		t.Errorf("after its next call the build reads\n%s\nwant 2m 40s (2m of work, 40s since) and no idle", row)
	}
}

// TestAWaitIsNeverIdle: a brief that waits for its approval has a quiet holder
// too, and reads its wait, not idle; nobody is meant to be working on it.
func TestAWaitIsNeverIdle(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	row := stepsRow(g, stepBrief)
	if strings.Contains(row, "idle") || !strings.Contains(row, "waiting for your approval 1h 00m") {
		t.Errorf("a brief waiting an hour reads\n%s\nwant its wait and no idle", row)
	}
}
