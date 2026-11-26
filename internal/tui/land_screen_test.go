package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// TestFiveOpensTheLandScreen: the key 5 opens the land screen from the ticket's
// screen and from a step's, whatever the cursor is on, and esc returns to where
// it was; the screens that have no ticket of their own ignore it, and the hint
// line lists it.
func TestFiveOpensTheLandScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("tab") // the question under "needs you"
	if ref := g.m.selected(); ref == nil || ref.kind != rowQuestion {
		t.Fatalf("the cursor is on %+v, want a question", ref)
	}
	if hint := lastLines(g.view(), 2); !strings.Contains(hint, "1-5 steps") {
		t.Errorf("the ticket's hint line does not list 5:\n%s", hint)
	}
	g.press("5")
	if g.m.Screen() != "land" || g.m.top().ticket != "JIG-1" {
		t.Fatalf("on %s, want land", g.m.Screen())
	}
	if head := strings.Split(g.view(), "\n")[0]; !strings.Contains(head, "› 5 land") {
		t.Errorf("the header does not name the screen: %s", head)
	}
	g.press("5") // already there
	if got := screens(g); len(got) != 3 || got[2] != "land" {
		t.Errorf("5 on the land screen left the stack %v", got)
	}
	g.press("esc")
	if g.m.Screen() != "ticket" {
		t.Fatalf("esc went to %s, want the ticket", g.m.Screen())
	}

	// From the build screen, with the cursor moved off its first row.
	g.tabTo("Steps").press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	g.press("down", "5")
	if g.m.Screen() != "land" {
		t.Fatalf("5 on the build screen went to %s", g.m.Screen())
	}
	if hint := lastLines(g.view(), 2); !strings.Contains(hint, "esc back") || !strings.Contains(hint, "1-5 steps") {
		t.Errorf("the land screen's hint line:\n%s", hint)
	}
	g.press("esc")
	if g.m.Screen() != "ticket" {
		t.Errorf("esc went to %s, want the ticket: a step key replaces the screens above it", g.m.Screen())
	}

	// From each other step's screen and from a slice's.
	for name, keys := range map[string][]string{
		"publish": {"up", "enter"}, "review": {"up", "up", "enter"}, "brief": {"home", "enter"},
	} {
		s := newRig(t, "JIG-2")
		s.feed(jig2()...).press(keys...)
		if s.m.Screen() != name {
			t.Fatalf("on %s, want %s", s.m.Screen(), name)
		}
		if s.press("5"); s.m.Screen() != "land" {
			t.Errorf("5 on the %s screen went to %s", name, s.m.Screen())
		}
	}
	s := newRig(t, "JIG-1")
	s.feed(jig1()...).press("enter", "enter") // the build screen, its first slice
	if s.m.Screen() != "slice" {
		t.Fatalf("on %s, want slice", s.m.Screen())
	}
	if s.press("5"); s.m.Screen() != "land" {
		t.Errorf("5 on the slice screen went to %s", s.m.Screen())
	}

	// A ticket the store has no folder for yet has nothing to land.
	waiting := awaitRig(t)
	waiting.press("5")
	if waiting.m.Screen() != "ticket" {
		t.Errorf("5 on a ticket that does not exist yet went to %s", waiting.m.Screen())
	}

	// The project's list and a chart's have no ticket of their own.
	main := mainRig(t)
	main.press("5")
	if main.m.Screen() != "main" {
		t.Errorf("5 on the project's list went to %s", main.m.Screen())
	}
}

// lastLines are the last n lines of a frame.
func lastLines(frame string, n int) string {
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// landTicket is a rig of JIG-2 (published, its pull request recorded) whose
// digest is d.
func landTicket(t *testing.T, d *digestInfo, lines ...events.Envelope) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.feed(jig2()...)
	for _, l := range lines {
		g.feed(l)
	}
	g.pin("JIG-2", d)
	g.syncDigests()
	return g
}

// TestLandScreenNamesEachMergeAndLanding: a ticket of two repos, one landed and
// one that waits, and a task two issues share that landed into one of them, each
// list the merge still to make with the host's word on it, and the landing with
// its time and who recorded it.
func TestLandScreenNamesEachMergeAndLanding(t *testing.T) {
	t.Parallel()
	publish := "done|pull request opened"
	repos := task("land", false, "done|a", "done|b", "done|c", publish, "current|waits for a merge")
	repos.PRs = []digestPR{
		{Repo: "api", Number: 7, URL: "https://x/7", State: "open", Target: "main"},
		{Repo: "web", Number: 3, URL: "https://x/3", State: "merged", Target: "main"},
		{Repo: "docs", Number: 2, URL: "https://x/2", State: "merged", Target: "main"},
	}
	g := landTicket(t, repos, jl(at(15, 30), "JIG-2", "", journal.EventLanded, func(l *journal.Line) {
		l.Repo, l.Base, l.Provenance = "web", "main", journal.ProvenancePerson
	}))
	g.press("5")
	view := g.view()
	golden(t, "land", view)
	for _, want := range []string{
		`◇ waits to land · say "Land JIG-2"`,
		"the session merges each pull request and records it",
		"1 of 3 landed",
		"◇ api PR #7 open into main merge to make",
		"✓ web PR #3 merged into main landed at 09:15:30 · by you",
		"◇ docs PR #2 merged into main merged on the host, not recorded",
		"record landed web into main", // the landed line, in the land step's own activity
	} {
		if !hasWords(view, want) {
			t.Errorf("two repos: the land screen lacks %q:\n%s", want, view)
		}
	}

	// A task two issues share: a pull request into each, one landed.
	shared := task("land", false, "done|a", "done|b", "done|c", publish, "current|waits for a merge")
	shared.PRs = []digestPR{
		{Repo: "shop", Number: 6, URL: "https://x/6", State: "unknown", Target: "502-second-issue"},
		{Repo: "shop", Number: 5, URL: "https://x/5", State: "merged", Target: "501-first-issue"},
	}
	g = landTicket(t, shared, jl(at(15, 30), "JIG-2", "", journal.EventLanded, func(l *journal.Line) {
		l.Repo, l.Into, l.Base, l.Provenance = "shop", "ISSUE-1", "501-first-issue", journal.ProvenanceHost
	}))
	g.send(tea.WindowSizeMsg{Width: 100, Height: 24}) // a long branch needs the room to be read whole
	g.press("5")
	view = g.view()
	for _, want := range []string{
		"✓ shop PR #5 merged into 501-first-issue landed at 09:15:30 · by the host",
		"◇ shop PR #6 into 502-second-issue (state not read) merge to make",
	} {
		if !strings.Contains(readWords(view), want) {
			t.Errorf("a shared task: the land screen lacks %q:\n%s", want, view)
		}
	}

	// A landing nobody's pull request names, written by an agent, and a ticket
	// published behind another one: its merge waits for the other to land.
	behind := task("land", false, "done|a", "done|b", "done|c", publish, "current|waits for a merge")
	behind.LandFirst = []string{"JIG-1"}
	behind.PRs = []digestPR{{Repo: "api", Number: 8, URL: "https://x/8", State: "open", Target: "main"}}
	g = landTicket(t, behind)
	g.press("5")
	if view := g.view(); !strings.Contains(readWords(view), "▲ PR #8 open into main merge to make") || !strings.Contains(view, "lands after JIG-1") {
		t.Errorf("a ticket published behind another:\n%s", view)
	}
	g = landTicket(t, task("land", false, "done|a", "done|b", "done|c", publish, "done|landed"), jl(at(15, 30), "JIG-2", "", journal.EventLanded, func(l *journal.Line) {
		l.Repo, l.Base, l.Provenance = "api", "release", journal.ProvenanceAgent
	}))
	g.press("5")
	if view := g.view(); !strings.Contains(readWords(view), "✓ api landed into release at 09:15:30 · by an agent") {
		t.Errorf("a landing no pull request names:\n%s", view)
	}
	merged := task("land", false, "done|a", "done|b", "done|c", publish, "current|merged on the host, not recorded")
	merged.PRs = []digestPR{{Repo: "api", Number: 8, URL: "https://x/8", State: "merged", Target: "main"}}
	g = landTicket(t, merged)
	g.press("5")
	if view := g.view(); !strings.Contains(view, "the session records it") || strings.Contains(view, "merges each pull request") {
		t.Errorf("a merge the host made:\n%s", view)
	}

	// A pull request closed on the host is not a merge to make.
	closedPR := task("land", false, "done|a", "done|b", "done|c", publish, "current|waits for a merge")
	closedPR.PRs = []digestPR{{Repo: "api", Number: 9, URL: "https://x/9", State: "closed", Target: "main"}}
	g = landTicket(t, closedPR)
	g.press("5")
	if view := g.view(); !strings.Contains(readWords(view), "✗ PR #9 closed into main closed on the host, not merged") {
		t.Errorf("a closed pull request:\n%s", view)
	}

	// Before the publish there is nothing to land: the step's words and an
	// empty panel. A closed ticket is offered no merge.
	g = newRig(t, "JIG-3")
	g.m.w.setRoster([]rosterRow{{ID: "JIG-3", Title: "T", State: "ready", Digest: task("review", true, "done|a", "done|b", "current|round 1 open", "pending|c", "pending|after the publish")}})
	g.press("5")
	if view := g.view(); !strings.Contains(view, "after the publish") || !strings.Contains(view, "no pull request to land yet") {
		t.Errorf("a ticket not published:\n%s", view)
	}
	closed := task("brief", false, "current|drafted", "pending|a", "pending|b", "done|closed · not planned", "done|none · closed without a pull request")
	closed.Closed = "not planned"
	closed.PRs = []digestPR{{Repo: "api", Number: 9, URL: "https://x/9", State: "open", Target: "main"}}
	g = landTicket(t, closed)
	g.press("5")
	if view := g.view(); strings.Contains(view, "merge PR #9") || !strings.Contains(view, "closed · not planned") || !strings.Contains(view, "closed without a pull request to merge") {
		t.Errorf("a closed ticket is offered a merge:\n%s", view)
	}
}

// TestLandingStopIsANeedsYouMoment: a ticket at the landing stop lists it in its
// needs-you block beside its questions and asks, and its chart row counts it, so
// the chart says the run needs you; a ticket that is published behind another, a
// landed one and a closed one are not a need.
func TestLandingStopIsANeedsYouMoment(t *testing.T) {
	t.Parallel()
	digests := endDigests()
	g := landTicket(t, digests[StateWaitsToLand])
	g.m.height = 30
	if page := pagesText(g.m.ticketPage("JIG-2")); !strings.Contains(page, "Needs you") || !strings.Contains(page, `◇ waits to land · say "Land JIG-2"`) {
		t.Errorf("the ticket screen has no needs-you row for the landing:\n%s", page)
	}
	// The row opens the land screen.
	var landing *rowRef
	for _, r := range g.m.ticketPage("JIG-2").refs() {
		if r.kind == rowLand {
			r := r
			landing = &r
		}
	}
	if landing == nil || landing.ticket != "JIG-2" {
		t.Errorf("the ticket screen has no row for the landing: %+v", landing)
	}
	// Two asks drawn whole take the rows the land step's block would have.
	g.send(tea.WindowSizeMsg{Width: 80, Height: 40})
	g.press("end")
	if v := g.view(); !strings.Contains(v, "╭─ Land ─") {
		t.Errorf("the cursor on the landing row does not open the land step's sub-steps:\n%s", v)
	}
	g.press("enter")
	if g.m.Screen() != "land" {
		t.Errorf("enter on the landing row went to %s", g.m.Screen())
	}

	for state, d := range digests {
		c := chartRig(t)
		c.m.w.tickets["JIG-4"].digest = d
		c.m.w.gen++
		ch := c.m.w.charts["checkout"]
		row := c.m.chartRows(ch)[0]
		want := state == StateWaitsToLand
		if row.needs != want {
			t.Errorf("%s: the chart row needs you = %v, want %v", state, row.needs, want)
		}
		if _, label := c.m.chartStatus(ch, c.m.chartRows(ch)); want && label != "run needs you" {
			t.Errorf("%s: the chart says %q, want it to say the run needs you", state, label)
		}
		if page := pagesText(c.m.ticketPage("JIG-4")); strings.Contains(page, "Needs you") != want {
			t.Errorf("%s: the ticket screen's needs-you block = %v, want %v:\n%s", state, !want, want, page)
		}
	}
}

// TestLandScreenRunsNothing: opening the land screen and every key on it leave the
// runner as it was: the screen never merges and never runs `jig land`, and the
// keys that would act on a ticket elsewhere (gate, publish, answer, keep, dismiss)
// say where landing is said instead.
func TestLandScreenRunsNothing(t *testing.T) {
	t.Parallel()
	g := landTicket(t, endDigests()[StateWaitsToLand])
	before := g.r.total()
	g.press("5")
	if g.m.Screen() != "land" {
		t.Fatalf("on %s, want land", g.m.Screen())
	}
	for _, key := range []string{"down", "up", "home", "end", "enter", "t", "5", "a", "k", "d", "g", "p", "y", "n"} {
		g.press(key)
		if g.m.Screen() != "land" && key != "t" {
			t.Fatalf("%s left the land screen for %s", key, g.m.Screen())
		}
		if n := g.r.total(); n != before {
			t.Fatalf("%s ran %v", key, g.r.calls[before:])
		}
		if g.m.ask != nil || g.m.input != nil {
			t.Fatalf("%s asked for something: %+v %+v", key, g.m.ask, g.m.input)
		}
	}
	g.press("g")
	if !strings.Contains(g.m.notice, `"Land JIG-2"`) {
		t.Errorf("the notice says %q, want where landing is said", g.m.notice)
	}
	for _, c := range g.r.calls {
		if len(c.Args) > 0 && (c.Args[0] == "land" || c.Args[0] == "merge") {
			t.Errorf("the screen ran %v", c.Args)
		}
	}
}
