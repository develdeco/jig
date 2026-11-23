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

// hasGroup is whether a ticket screen draws its "needs you" group: the heading on
// a line of its own, which no phase's words are.
func hasGroup(page string) bool {
	for _, l := range strings.Split(page, "\n") {
		if strings.TrimSpace(l) == "needs you" {
			return true
		}
	}
	return false
}

// waitingOnAnswer is the roster's word that n slices of the ticket wait on an
// answer, as `jig next` gives it once the store has moved.
func waitingOnAnswer(g *rig, id string, n int) {
	d := *g.m.w.tickets[id].digest
	slices := *d.Slices
	slices.NeedsInput = n
	d.Slices = &slices
	g.pin(id, &d)
	g.m.w.tickets[id].digest = &d
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

		// The window's edge: a minute old is still live, a second more is not.
		now = rigNow.Add(30 * time.Second)
		if hasGroup(ticketPage()) {
			t.Errorf("a line exactly one minute old is not live")
		}
		now = rigNow.Add(31 * time.Second)
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
// header show it. What a live session records itself (the asks of a round it is
// reviewing) is held back.
func TestAnOpenQuestionIsNeverHeldBackByALiveBuilder(t *testing.T) {
	t.Parallel()
	g := askedRig(t, "JIG-1")
	g.feed(ll(at(59, 0), "JIG-1", journal.EventTool, with("s-builder", "", "", "", "", "")))
	if p := pagesText(g.m.ticketPage("JIG-1")); !hasGroup(p) || !strings.Contains(p, "Formal or casual?") {
		t.Errorf("a live builder hides its own question on the ticket screen:\n%s", p)
	}
	if p := pagesText(g.m.mainPage()); !strings.Contains(p, "1 open question") {
		t.Errorf("main does not count the question of a live builder:\n%s", p)
	}

	c := chartScreenRig(t)
	c.feed(jl(at(30, 0), "JIG-5", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-009", "agent")))
	waitingOnAnswer(c, "JIG-5", 1)
	c.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-builder", "", "", "", "", "")))
	if v := c.view(); !strings.Contains(v, "run needs you") {
		t.Errorf("the chart header hides the question of a live builder:\n%s", v)
	}

	// The asks of a round a live session is reviewing are held back.
	r := newRig(t, "JIG-2")
	r.feed(jig2Mid()...)
	r.feed(ll(at(59, 0), "JIG-2", journal.EventTool, with("s-reviewer", "", "", "", "", "")))
	if p := pagesText(r.m.ticketPage("JIG-2")); hasGroup(p) {
		t.Errorf("a live session's own round holds its asks up:\n%s", p)
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
		if p := pagesText(g.m.mainPage()); strings.Contains(p, "open question") {
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
