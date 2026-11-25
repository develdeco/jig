package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// newestChainLines are one ticket's lines from its build to its publish: two
// commits of slice a that failed their oracle, the rounds that sent two fix
// slices back, and the pull request. They are written oldest first.
func newestChainLines() []events.Envelope {
	run := func(min, sec int, commit byte) events.Envelope {
		return jl(at(min, sec), "JIG-6", "a", journal.EventOracle, with("", sha(commit), "fail", "", "", ""), func(l *journal.Line) { l.Command, l.Seconds = "go test ./...", 3 })
	}
	lines := []events.Envelope{
		jl(at(20, 0), "JIG-6", "a", journal.EventVerifyPending, with("s-builder", sha('b'), "", "first try", "", "")),
		run(20, 30, 'b'),
		jl(at(21, 0), "JIG-6", "a", journal.EventVerifyPending, with("s-builder", sha('c'), "", "second try", "", "")),
		run(21, 30, 'c'),
	}
	lines = append(lines, fixSliceLines()...)
	return append(lines,
		jl(at(33, 0), "JIG-6", "", journal.EventPrBody, func(l *journal.Line) { l.Commit, l.Branch = sha('c'), "jig/JIG-6" }),
		jl(at(33, 10), "JIG-6", "", journal.EventPublishApproved, func(l *journal.Line) {
			l.Commit, l.Branch, l.Provenance = sha('c'), "jig/JIG-6", "person"
		}),
		jl(at(33, 20), "JIG-6", "", journal.EventPr, func(l *journal.Line) {
			l.Commit, l.Branch, l.Outcome, l.URL = sha('c'), "jig/JIG-6", "opened", "https://example.test/acme/demo/pull/7"
		}),
		jl(at(33, 21), "JIG-6", "", journal.EventPublishDone),
	)
}

// TestEveryListReadsNewestFirst is the rule on every screen at once: one
// ticket's lines are delivered newest first, and then each screen that lists
// what happened draws it newest first - the ticket's activity, the build's
// commits, the review's rounds and fix slices, the publish activity, a slice's
// timeline, the chart's activity and the tail.
func TestEveryListReadsNewestFirst(t *testing.T) {
	t.Parallel()
	chainRig := func() *rig {
		g := newRig(t, "JIG-6")
		g.r.answer("status JIG-6 --store /s", fixSliceStatus(true), 0)
		g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
		g.feed(backwards(newestChainLines())...)
		return g
	}

	// The ticket: its Activity, then the tail under it.
	g := chainRig()
	newestOnTop(t, "ticket activity", clocksOf(panelRows(g.view(), "Activity")), 5)
	g.press("t")
	newestOnTop(t, "ticket tail", clocksOf(tailRows(g.view())), 5)
	g.press("t")

	// The build: the commits, newest begun first, each with its runs.
	g.press("home", "down", "enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	inOrder(t, "build commits", pageText(g), "second try", "first try")

	// The slice: its timeline, and the build's activity.
	newestOnTop(t, "build activity", clocksOf(panelRows(g.view(), "Build activity")), 3)
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want slice", g.m.Screen())
	}
	_, timeline, _ := strings.Cut(pageText(g), "Timeline\n")
	newestOnTop(t, "slice timeline", clocksOf(strings.Split(timeline, "\n")), 4)
	g.press("esc", "esc")

	// The review: the rounds, the fix slices they sent back, and the activity.
	g.press("home", "down", "down", "enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	text := pageText(g)
	inOrder(t, "rounds", readWords(text), "round 2 ", "round 1 ")
	inOrder(t, "fix slices", readWords(text), "Sent back", "fix-2 round 2", "fix-1 round 1")
	newestOnTop(t, "review activity", clocksOf(panelRows(g.view(), "Review activity")), 4)
	g.press("esc")

	// The publish: the activity.
	g.press("home", "down", "down", "down", "enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want publish", g.m.Screen())
	}
	rows := clocksOf(panelRows(g.view(), "Publish activity"))
	newestOnTop(t, "publish activity", rows, 3)
	if rows[0] != "09:33:21" {
		t.Errorf("the publish activity starts at %s, want 09:33:21", rows[0])
	}

	// The chart: the Activity of its tickets.
	chartLines, _ := chartTickets()
	chart2 := newChartBackwards(t, chartLines)
	chart2.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	chart2.openChart("checkout")
	newestOnTop(t, "chart activity", clocksOf(panelRows(chart2.view(), "Activity")), 3)
}

// newChartBackwards is chartRig that is fed the chart's lines newest first.
func newChartBackwards(t *testing.T, lines []events.Envelope) *rig {
	t.Helper()
	g := newRig(t, "")
	_, status := chartTickets()
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(), status)
	return g.feed(backwards(lines)...)
}
