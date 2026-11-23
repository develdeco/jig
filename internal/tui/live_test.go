package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

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
	const quietMain = "1 open question"

	t.Run("the ticket screen and main", func(t *testing.T) {
		g := askedRig(t, "JIG-1")
		now := rigNow
		g.m.now = func() time.Time { return now }
		ticketPage := func() string { return pagesText(g.m.ticketPage("JIG-1")) }
		mainPage := func() string { return pagesText(g.m.mainPage()) }

		if !hasGroup(ticketPage()) || !strings.Contains(mainPage(), quietMain) {
			t.Fatalf("with no session at work the screens do not ask for the person:\n%s\n%s", ticketPage(), mainPage())
		}

		// A tool call 30 seconds ago: live.
		g.feed(ll(at(59, 0), "JIG-1", journal.EventTool, with("s-builder", "", "", "", "", "")))
		if p := ticketPage(); hasGroup(p) {
			t.Errorf("the ticket screen asks for the person while a session is at work:\n%s", p)
		}
		if p := mainPage(); strings.Contains(p, quietMain) {
			t.Errorf("main counts the open question while a session is at work:\n%s", p)
		}
		// The question itself stays a row of the screen that answers it.
		if p := pagesText(g.m.slicePage("JIG-1", "a")); !strings.Contains(p, "q-1") {
			t.Errorf("the slice screen lost the question:\n%s", p)
		}

		// The window's edge: a minute old is still live, a second more is not.
		now = rigNow.Add(30 * time.Second)
		if hasGroup(ticketPage()) {
			t.Errorf("a line exactly one minute old is not live")
		}
		now = rigNow.Add(31 * time.Second)
		if !hasGroup(ticketPage()) || !strings.Contains(mainPage(), quietMain) {
			t.Errorf("the words did not come back once the line went quiet:\n%s\n%s", ticketPage(), mainPage())
		}

		// A session end after the line: not live, even inside the window.
		now = rigNow
		g.feed(ll(at(59, 10), "JIG-1", journal.EventSessionEnd, with("s-builder", "", "", "", "", "")))
		if !hasGroup(ticketPage()) {
			t.Errorf("a session that ended still holds the words back:\n%s", ticketPage())
		}
		// The same session writing again after its end is at work again.
		g.feed(ll(at(59, 15), "JIG-1", journal.EventTool, with("s-builder", "", "", "", "", "")))
		if hasGroup(ticketPage()) {
			t.Errorf("a session that wrote after its end does not count:\n%s", ticketPage())
		}
		// Another session's line is live too.
		g.feed(ll(at(59, 20), "JIG-1", journal.EventSessionTouch, with("s-other", "", "", "", "", "")))
		if hasGroup(ticketPage()) {
			t.Errorf("a second session's touch does not count:\n%s", ticketPage())
		}
		// A line that is no sign of a session at work is not one.
		g2 := askedRig(t, "JIG-1")
		g2.feed(ll(at(59, 20), "JIG-1", journal.EventNoteDelivered, with("s-builder", "", "", "", "", "")))
		if !hasGroup(pagesText(g2.m.ticketPage("JIG-1"))) {
			t.Errorf("a delivered note held the words back")
		}
	})

	t.Run("the chart's header and Run panel", func(t *testing.T) {
		g := chartScreenRig(t)
		g.feed(jl(at(30, 0), "JIG-5", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-009", "agent")))
		waitingOnAnswer(g, "JIG-5", 1)
		g.send(tea.WindowSizeMsg{Width: 100, Height: 60})
		if v := g.view(); !strings.Contains(v, "run needs you") || !strings.Contains(v, `say "Continue checkout"`) {
			t.Fatalf("with no session at work the chart does not ask for the person:\n%s", v)
		}

		g.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-builder", "", "", "", "", "")))
		v := g.view()
		if strings.Contains(v, "run needs you") || !strings.Contains(v, "running") {
			t.Errorf("the chart header does not say running:\n%s", v)
		}
		if !strings.Contains(v, "running · a session worked on it 30s ago") || strings.Contains(v, `say "Continue checkout"`) {
			t.Errorf("the Run panel does not say a session is at work:\n%s", v)
		}
	})
}

// TestALandPromptIsNeverHeldBack: a landing's prompt is the person's word and a
// session merges at it, so a live session does not hide it, and the header still
// says the landing waits for the person.
func TestALandPromptIsNeverHeldBack(t *testing.T) {
	t.Parallel()
	g := tallChart(t, checkoutJSON(atLanding(landRow("JIG-4", "waits for a merge"))))
	g.feed(ll(at(59, 0), "JIG-5", journal.EventTool, with("s-builder", "", "", "", "", "")))
	v := g.view()
	if !strings.Contains(v, `say "Land JIG-4"`) || strings.Contains(v, "a session worked on it") {
		t.Errorf("a live session held back the landing's prompt:\n%s", v)
	}
	if !strings.Contains(v, "landing waits for you") {
		t.Errorf("the header does not say the landing waits for you:\n%s", v)
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
		if v := g.view(); !strings.Contains(v, "run needs you") {
			t.Fatalf("the chart does not ask for the person:\n%s", v)
		}
		g.feed(jl(at(30, 20), "JIG-5", "c", journal.EventAnswer, with("s-person", "", "", "Casual.", "q-009", "person")))
		waitingOnAnswer(g, "JIG-5", 0)
		if v := g.view(); strings.Contains(v, "run needs you") {
			t.Errorf("the chart header still says run needs you after the answer:\n%s", v)
		}
	})
}
