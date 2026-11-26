package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// reviewedRig is a ticket whose first round left two asks for the person.
func reviewedRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	return g
}

// askedRig is a ticket with one question open on slice a and nothing else.
func askedRig(t *testing.T, ticket string) *rig {
	t.Helper()
	g := newRig(t, ticket)
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "needs-input", "1", "-", "q-1", "Greet", "app", "unit", "-"}), 0)
	g.feed(
		jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(0, 30), "JIG-1", "a", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-1", "agent")),
	)
	return g
}

// hasGroup is whether a ticket screen draws its "Needs you" panel.
func hasGroup(page string) bool { return hasLine(page, "Needs you") }

// waitingOnAnswer is the roster's word that n slices of the ticket wait on an
// answer, as `jig next` gives it once the store has moved.
func waitingOnAnswer(g *rig, id string, n int) {
	d := *g.m.w.tickets[id].digest
	slices := *d.Slices
	slices.NeedsInput = n
	d.Slices = &slices
	g.pin(id, &d)
	g.m.w.tickets[id].digest = &d
	g.m.w.gen++
}

// TestNeedsYouIsHeldBackWhileASessionIsLive: a ledger line of a session bound to
// the ticket within the window holds back the words that tell the person to act
// (the ticket screen's group, main's open-question count, the chart's header and
// Run panel), and they come back when the line goes quiet, at the window's edge
// or when the session ends.
func TestNeedsYouIsHeldBackWhileASessionIsLive(t *testing.T) {
	t.Parallel()

	t.Run("the ticket screen and main", func(t *testing.T) {
		g := reviewedRig(t)
		now := rigNow
		g.m.now = func() time.Time { return now }
		ticketPage := func() string { return pagesText(g.m.ticketPage("JIG-2")) }

		if !hasGroup(ticketPage()) {
			t.Fatalf("with no session at work the screen does not ask for the person:\n%s", ticketPage())
		}

		// A tool call 30 seconds ago: live.
		g.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-builder", "", "", "", "", "")))
		if p := ticketPage(); hasGroup(p) {
			t.Errorf("the ticket screen asks for the person while a session is at work:\n%s", p)
		}
		// The asks themselves stay rows of the screen that decides them.
		if p := pagesText(g.m.reviewPage("JIG-2")); !strings.Contains(p, "r1-f1") {
			t.Errorf("the review screen lost the asks:\n%s", p)
		}

		// The window's edge: a window old is still live, a second more is not.
		now = rigNow.Add(liveWindow - 30*time.Second)
		if hasGroup(ticketPage()) {
			t.Errorf("a line exactly one window old is still live")
		}
		now = rigNow.Add(liveWindow - 29*time.Second)
		if !hasGroup(ticketPage()) {
			t.Errorf("the words did not come back once the line went quiet:\n%s", ticketPage())
		}

		// A session end after the line: not live, even inside the window.
		now = rigNow
		g.feed(ll(at(59, 10), "", journal.EventSessionEnd, with("s-builder", "", "", "", "", "")))
		if !hasGroup(ticketPage()) {
			t.Errorf("a session that ended still holds the words back:\n%s", ticketPage())
		}
		// The same session writing again after its end is at work again.
		g.feed(ll(at(59, 15), "JIG-2", journal.EventTool, with("s-builder", "", "", "", "", "")))
		if hasGroup(ticketPage()) {
			t.Errorf("a session that wrote after its end does not count:\n%s", ticketPage())
		}
		// Another session's line is live too.
		g.feed(ll(at(59, 20), "JIG-2", journal.EventSessionTouch, with("s-other", "", "", "", "", "")))
		if hasGroup(ticketPage()) {
			t.Errorf("a second session's touch does not count:\n%s", ticketPage())
		}
		// A line that is no sign of a session at work is not one.
		g2 := reviewedRig(t)
		g2.feed(ll(at(59, 20), "JIG-2", journal.EventNoteDelivered, with("s-builder", "", "", "", "", "")))
		if !hasGroup(pagesText(g2.m.ticketPage("JIG-2"))) {
			t.Errorf("a delivered note held the words back")
		}
	})

	t.Run("the chart's header and Run panel", func(t *testing.T) {
		g := chartScreenRig(t)
		// The published ticket has landed, so no landing waits for the person.
		g.pin("JIG-4", task("land", false, "done|approved by Ada", "done|1 slice green", "done|round 1 clean", "done|pull request opened", "done|landed"))
		g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
		if v := g.view(); strings.Contains(v, "running") || !strings.Contains(v, `say "Continue checkout"`) {
			t.Fatalf("with no session at work the chart says running:\n%s", v)
		}

		g.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-builder", "", "", "", "", "")))
		v := g.view()
		if head, _, _ := strings.Cut(v, "\n"); strings.Contains(head, "in progress") || !strings.Contains(head, "running") {
			t.Errorf("the chart header does not say running:\n%s", v)
		}
		if !strings.Contains(v, "running · a session worked on it 30s ago") || strings.Contains(v, `say "Continue checkout"`) {
			t.Errorf("the Run panel does not say a session is at work:\n%s", v)
		}
	})
}

// TestASessionEndReachesTheTicketsOfItsSession: the end of a session names no
// ticket, as the writers record it, and still ends the sessions of every ticket
// it worked on, so the words come back at once and not when the line ages out.
func TestASessionEndReachesTheTicketsOfItsSession(t *testing.T) {
	t.Parallel()
	g := reviewedRig(t)
	g.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-builder", "", "", "", "", "")))
	if hasGroup(pagesText(g.m.ticketPage("JIG-2"))) {
		t.Fatalf("a live session does not hold the words back")
	}
	g.feed(ll(at(59, 10), "", journal.EventSessionEnd, with("s-builder", "", "", "", "", "")))
	if p := pagesText(g.m.ticketPage("JIG-2")); !hasGroup(p) {
		t.Errorf("a session that ended with a line naming no ticket still holds the words back:\n%s", p)
	}
}

// TestALandPromptIsNeverHeldBack: a landing's prompt is the person's word and a
// session merges at it, so a live session does not hide it, and the header still
// says the landing waits for the person.
func TestALandPromptIsNeverHeldBack(t *testing.T) {
	t.Parallel()
	g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-4", "waits for a merge"))))
	g.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-builder", "", "", "", "", "")))
	v := g.view()
	if !strings.Contains(v, `say "Land JIG-4"`) || strings.Contains(v, "a session worked on it") {
		t.Errorf("a live session held back the landing's prompt:\n%s", v)
	}
	if !strings.Contains(v, "landing waits for you") {
		t.Errorf("the header does not say the landing waits for you:\n%s", v)
	}
}

// TestAnOpenQuestionIsNeverHeldBackByALiveBuilder: a builder that asked with
// `jig ask` and carries on keeps its ticket live, and its question is still the
// person's to answer: the ticket screen's group, main's count and the chart's
// header show it. The asks of a round a live session is reviewing stay too, under
// an asks group (TestALiveSessionKeepsTheRoundsAsksOnScreen).
func TestAnOpenQuestionIsNeverHeldBackByALiveBuilder(t *testing.T) {
	t.Parallel()
	g := askedRig(t, "JIG-1")
	g.feed(ll(at(59, 0), "JIG-1", journal.EventTool, with("s-builder", "", "", "", "", "")))
	if p := pagesText(g.m.ticketPage("JIG-1")); !hasGroup(p) || !strings.Contains(p, "Formal or casual?") {
		t.Errorf("a live builder hides its own question on the ticket screen:\n%s", p)
	}
	if p := g.mainText(); !strings.Contains(p, "1 open question") {
		t.Errorf("main does not count the question of a live builder:\n%s", p)
	}

	c := chartScreenRig(t)
	c.feed(jl(at(30, 0), "JIG-5", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-009", "agent")))
	waitingOnAnswer(c, "JIG-5", 1)
	c.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-builder", "", "", "", "", "")))
	if v := c.view(); !strings.Contains(v, "run needs you") {
		t.Errorf("the chart header hides the question of a live builder:\n%s", v)
	}

	// The asks of a round a live session is reviewing do not make the needs-you group.
	r := newRig(t, "JIG-2")
	r.feed(jig2Mid()...)
	r.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-reviewer", "", "", "", "", "")))
	if p := pagesText(r.m.ticketPage("JIG-2")); hasGroup(p) {
		t.Errorf("a live session's own round draws the needs-you group:\n%s", p)
	}
}

// TestALiveSessionKeepsTheRoundsAsksOnScreen: the asks of a recorded round stay
// on the ticket screen, text and controls, while a session is live, since the
// person decides them from there whoever else is at work; their group reads
// asks then, and needs you again once no session is live.
func TestALiveSessionKeepsTheRoundsAsksOnScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	g.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-reviewer", "", "", "", "", "")))
	p := pagesText(g.m.ticketPage("JIG-2"))
	if hasGroup(p) || !hasLine(p, "Asks") || !strings.Contains(p, "Unchecked error") {
		t.Fatalf("a live session's round does not keep its asks under the asks group:\n%s", p)
	}
	g.press("enter") // the review step: the cursor lands on the first ask
	g.press("esc").tabTo("Asks").press("end", "d")
	if got := g.r.lines(); len(got) != 1 || got[0] != "triage JIG-2 r1-f2 dismiss --store /s" {
		t.Errorf("d on a live ticket's ask ran %q", got)
	}

	q := newRig(t, "JIG-2")
	q.feed(jig2Mid()...)
	q.m.now = func() time.Time { return rigNow.Add(2 * liveWindow) }
	if p := pagesText(q.m.ticketPage("JIG-2")); !hasGroup(p) {
		t.Errorf("with no session live the group does not read needs you:\n%s", p)
	}
}

// TestHeldBackNeedsYouLeavesTheHeaderToo: while a ticket's needs-you is held back
// its header does not say findings wait, and it does when the line is quiet.
func TestHeldBackNeedsYouLeavesTheHeaderToo(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	header := func() string { return strings.Join(g.m.header(80), "\n") }
	if !strings.Contains(header(), "review findings") {
		t.Fatalf("a quiet ticket's header does not say findings wait:\n%s", header())
	}
	g.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-reviewer", "", "", "", "", "")))
	if h := header(); strings.Contains(h, "review findings") {
		t.Errorf("the header says findings wait while the group is held back:\n%s", h)
	}
}

// TestEndsRowsAtALandingStopReadAsPhaseWritesThem: in an issue chart the end of
// a run is a ticket that waits to land into the issue, in the words the verb
// carries from internal/phase, not a wait to merge.
func TestEndsRowsAtALandingStopReadAsPhaseWritesThem(t *testing.T) {
	t.Parallel()
	const fact = "JIG-7 waits for JIG-4 to land into 526-the-issue"
	g := tallChart(t, checkoutJSON(atLanding(landingRow("JIG-4", "waits for a merge")), func(v map[string]any) {
		v["ends"] = []map[string]any{{"ticket": "JIG-7", "blocker": "JIG-4", "kind": "merged", "fact": fact, "prs": []map[string]any{pull("7", "OPEN")}}}
	}))
	v := g.view()
	if !strings.Contains(v, fact) || strings.Contains(v, "to merge") {
		t.Errorf("the ends row does not read as phase writes it:\n%s", v)
	}
}

// TestAnIssuesEndsReadAsPhaseWritesThemWhateverTheLandings: a chart that plans an
// issue words every non-stacked end as the verb carries it, once, before any
// landing is open too; a stacked end keeps its retarget words, and a legacy
// chart keeps the merge wait.
func TestAnIssuesEndsReadAsPhaseWritesThemWhateverTheLandings(t *testing.T) {
	t.Parallel()
	const fact = "JIG-7 waits for JIG-4 to land"
	end := func(kind string) func(map[string]any) {
		return func(v map[string]any) {
			v["ends"] = []map[string]any{{"ticket": "JIG-7", "blocker": "JIG-4", "kind": kind, "fact": fact, "prs": []map[string]any{pull("7", "OPEN")}}}
		}
	}
	model := func(v map[string]any) { v["model"] = true }

	g := tallChart(t, checkoutJSON(model, end("merged")))
	v := g.view()
	if !strings.Contains(v, "ends at "+fact) || strings.Contains(v, "to merge") || strings.Count(v, fact) != 1 {
		t.Errorf("an issue's run with no landing open does not say its end once, as phase writes it:\n%s", v)
	}
	g = tallChart(t, checkoutJSON(model, end("stacked")))
	if v := g.view(); !strings.Contains(v, "JIG-7 retargets when JIG-4 merges") || strings.Contains(v, fact) {
		t.Errorf("a stacked end lost its retarget words:\n%s", v)
	}
	g = tallChart(t, checkoutJSON(end("merged")))
	if v := g.view(); !strings.Contains(v, "JIG-7 waits for JIG-4 to merge · PR #7 open") {
		t.Errorf("a legacy chart's merge wait changed:\n%s", v)
	}
}

// TestALiveTicketsOwnAsksAndApprovalsStayHeldBackOnTheChart: a ticket whose
// needs-you is a round's asks does not count as needing the person on the chart
// while a session is at work on it, and does when the line is quiet; a question
// on a brief counts whatever else is at work; and a live ticket at findings that
// also has an open question keeps the step's own words in its header.
func TestALiveTicketsOwnAsksAndApprovalsStayHeldBackOnTheChart(t *testing.T) {
	t.Parallel()
	needsOf := func(g *rig, id string) bool {
		for _, r := range g.m.chartRows(g.m.w.charts[g.m.top().chart]) {
			if r.id == id {
				return r.needs
			}
		}
		t.Fatalf("no row for %s", id)
		return false
	}
	// JIG-5 stands at its first round's asks, JIG-4 has landed.
	g := newRig(t, "")
	lines, status := chartTickets()
	lines = append(asTicket(jig2(), "JIG-4"), asTicket(jig2Mid(), "JIG-5")...)
	status["JIG-5"] = relabelled(statusJIG2(), "JIG-2", "JIG-5")
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(), status)
	g.feed(lines...)
	g.pin("JIG-4", task("land", false, "done|approved by Ada", "done|1 slice green", "done|round 1 clean", "done|pull request opened", "done|landed"))
	g.openChart("checkout")
	g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
	if !hasNeedsPhase(g, "JIG-5") {
		t.Fatalf("the fixture's ticket has no phase waiting on the person")
	}
	if !needsOf(g, "JIG-5") {
		t.Fatalf("a quiet ticket with asks does not need the person")
	}
	g.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-reviewer", "", "", "", "", "")))
	if needsOf(g, "JIG-5") {
		t.Errorf("a live ticket's own asks count on the chart's rows")
	}
	if head, _, _ := strings.Cut(g.view(), "\n"); !strings.Contains(head, "running") {
		t.Errorf("the chart header does not say running:\n%s", head)
	}
}

// hasNeedsPhase is whether the ticket's phases say something waits on the person.
func hasNeedsPhase(g *rig, id string) bool {
	return needsYou(g.m.w.tickets[id], g.m.phaseOf(g.m.w.tickets[id]))
}

// TestAQuestionOnABriefCountsWhileASessionIsLive: a live planner's open question
// on its brief, with no slice in needs-input, is still the person's, and a live
// ticket at findings that also has an open question keeps the step's own words.
func TestAQuestionOnABriefCountsWhileASessionIsLive(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.feed(jl(at(30, 0), "JIG-8", "", journal.EventQuestion, with("s-planner", "", "", "Which discount?", "q-brief", "agent")))
	g.feed(ll(at(59, 0), "JIG-8", journal.EventTool, with("s-planner", "", "", "", "", "")))
	c := g.m.w.charts[g.m.top().chart]
	for _, r := range g.m.chartRows(c) {
		if r.id == "JIG-8" && !r.needs {
			t.Errorf("a live planner's open question does not count on the chart's row")
		}
	}

	r := newRig(t, "JIG-2")
	r.feed(jig2Mid()...)
	r.feed(jl(at(12, 30), "JIG-2", "a", journal.EventQuestion, with("s-builder", "", "", "Keep it?", "q-x", "agent")))
	r.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-reviewer", "", "", "", "", "")))
	if h := strings.Join(r.m.header(80), "\n"); strings.Contains(h, "in progress") {
		t.Errorf("a live ticket with an open question hides its step's own words:\n%s", h)
	}
}

// TestNeedsYouEndsWithItsCause: once the answer to a question or the decision of
// an ask is journaled, the next frame has none of its "needs you": not the ticket
// screen's group, not main's count, not the chart's header.
func TestNeedsYouEndsWithItsCause(t *testing.T) {
	t.Parallel()

	t.Run("a question answered", func(t *testing.T) {
		g := askedRig(t, "JIG-1")
		if p := pagesText(g.m.ticketPage("JIG-1")); !hasGroup(p) {
			t.Fatalf("the question does not ask for the person:\n%s", p)
		}
		g.feed(jl(at(1, 0), "JIG-1", "a", journal.EventAnswer, with("s-person", "", "", "Casual.", "q-1", "person")))
		if p := pagesText(g.m.ticketPage("JIG-1")); hasGroup(p) {
			t.Errorf("the ticket screen still asks after the answer:\n%s", p)
		}
		if p := g.mainText(); strings.Contains(p, "open question") {
			t.Errorf("main still counts the question after the answer:\n%s", p)
		}
	})

	t.Run("every ask decided", func(t *testing.T) {
		g := newRig(t, "JIG-2")
		g.feed(jig2Mid()...)
		if p := pagesText(g.m.ticketPage("JIG-2")); !hasGroup(p) {
			t.Fatalf("the asks do not ask for the person:\n%s", p)
		}
		g.feed(
			jl(at(12, 0), "JIG-2", "", journal.EventTriage, with("s-person", "", "dismiss", "", "r1-f1", "person")),
			jl(at(12, 5), "JIG-2", "", journal.EventTriage, with("s-person", "", "keep", "", "r1-f2", "person")),
		)
		if p := pagesText(g.m.ticketPage("JIG-2")); hasGroup(p) {
			t.Errorf("the ticket screen still asks after the last decision:\n%s", p)
		}
	})

	t.Run("the chart header", func(t *testing.T) {
		g := chartScreenRig(t)
		g.feed(jl(at(30, 0), "JIG-5", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-009", "agent")))
		waitingOnAnswer(g, "JIG-5", 1)
		needsOf := func(id string) bool {
			for _, r := range g.m.chartRows(g.m.w.charts[g.m.top().chart]) {
				if r.id == id {
					return r.needs
				}
			}
			return false
		}
		if !needsOf("JIG-5") {
			t.Fatalf("the chart does not ask for the person:\n%s", g.view())
		}
		g.feed(jl(at(30, 20), "JIG-5", "c", journal.EventAnswer, with("s-person", "", "", "Casual.", "q-009", "person")))
		waitingOnAnswer(g, "JIG-5", 0)
		if needsOf("JIG-5") {
			t.Errorf("the chart still counts the answered ticket as needing the person:\n%s", g.view())
		}
	})
}
