package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/phase"
)

// pagesText is a page's lines, with no marker, one to a row.
func pagesText(p page) string {
	lines := make([]string, len(p.lines))
	for i, l := range p.lines {
		lines[i] = l.text
	}
	return strings.Join(lines, "\n")
}

// task is the digest of a task standing at current with the five phases' states
// and words, each "state|text".
func task(current string, ready bool, phases ...string) *digestInfo {
	d := &digestInfo{Kind: "task", Current: current, Ready: ready}
	for i, n := range []string{"brief", "build", "review", "publish", "land"} {
		state, text, _ := strings.Cut(phases[i], "|")
		d.Phases = append(d.Phases, digestPhase{Name: n, State: state, Text: text})
	}
	return d
}

// TestStepsTakeTheDigestsWords: a ticket's steps, where the main and ticket
// screens stand it, and the Land line under its steps are the words of the
// digest `jig next` gave, for a ticket in each of the states the audit found.
func TestStepsTakeTheDigestsWords(t *testing.T) {
	t.Parallel()
	const target = "501-review-rounds-stop-repeating-work"
	pending := []string{"pending|after the brief", "pending|after the build", "pending|after a clean review", "pending|after the publish"}
	published := func(land string, prs ...digestPR) *digestInfo {
		d := task("land", false, "done|approved by Ada", "done|2 slices green", "done|round 1 clean", "done|pull request opened · https://example.test/acme/demo/pull/503", land)
		d.PRs = prs
		return d
	}
	pr := digestPR{Repo: "demo", Number: 503, URL: "https://example.test/acme/demo/pull/503", State: "open", Target: target}
	closed := task("publish", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
	closed.Closed = "covered by develdeco/jig#82"
	issue := &digestInfo{Kind: "issue", State: "waiting"}
	child := task("build", true, "done|approved by Ada", "current|ready to build", pending[2], pending[3], "pending|after the publish")
	child.Parent = "GATE-49"

	for _, tc := range []struct {
		name string
		d    *digestInfo
		// land is the words the Land line must hold; "" says it has none.
		land string
		kind string
		// standing is the work phase the steps stand at.
		standing string
	}{
		{"a new ticket", task("brief", false, "current|being drafted", pending[0], pending[1], pending[2], "pending|after the publish"), "", "", "brief"},
		{"a drafted brief", task("brief", false, "current|drafted · waiting for your approval", pending[0], pending[1], pending[2], "pending|after the publish"), "", "", "brief"},
		{"a build with a failed slice", task("build", false, "done|approved by Ada", "current|1 of 2 green · slice s2 failed its last run", pending[1], pending[2], "pending|after the publish"), "", "", "build"},
		{"a review under way", task("review", true, "done|approved by Ada", "done|2 slices green", "current|round 1 open", pending[2], "pending|after the publish"), "", "", "review"},
		{"a published ticket with an open pull request", published("current|waits for a merge", pr), "waits for a merge · PR #503 into " + target, "", "publish"},
		{"a merge the host made and nobody recorded", published("current|merged on the host, not recorded", pr), "merged on the host, not recorded", "", "publish"},
		{"a landed ticket", published("done|landed", pr), "landed", "", "publish"},
		{"a closed ticket", closed, "none · closed without a pull request", "", "publish"},
		{"an issue", issue, "", "issue", "brief"},
		{"a task of an issue", child, "", "task of GATE-49", "build"},
	} {
		g := newRig(t, "")
		g.m.w.setRoster([]rosterRow{{ID: "T-1", Title: "Ticket", State: "ready", Digest: tc.d}})
		tk := g.m.w.tickets["T-1"]

		st, ok := g.m.Standing("T-1")
		if !ok || st.Current != tc.standing {
			t.Errorf("%s: stands at %+v, want %s", tc.name, st, tc.standing)
		}
		if len(tc.d.Phases) > 0 {
			for i, row := range g.m.steps(tk) {
				if want := tc.d.Phases[i]; row.Text != want.Text || row.Done != (want.State == phase.StateDone) {
					t.Errorf("%s: step %d is %+v, the digest says %+v", tc.name, i+1, row, want)
				}
			}
			if tc.d.Ready != st.Ready {
				t.Errorf("%s: ready is %v, the digest says %v", tc.name, st.Ready, tc.d.Ready)
			}
		}

		page := pagesText(g.m.ticketPage("T-1"))
		if tc.land == "" && strings.Contains(page, "land ") {
			t.Errorf("%s: the ticket screen has a Land line:\n%s", tc.name, page)
		}
		if tc.land != "" && !strings.Contains(page, "land  "+tc.land) {
			t.Errorf("%s: the ticket screen has no Land line %q:\n%s", tc.name, tc.land, page)
		}
		if row := pagesText(g.m.mainPage()); tc.kind != "" && !strings.Contains(row, tc.kind) {
			t.Errorf("%s: the main screen's row does not name %q:\n%s", tc.name, tc.kind, row)
		}
	}

	// The header of a ticket screen names the kind and the issue.
	g := newRig(t, "T-1")
	g.m.w.setRoster([]rosterRow{{ID: "T-1", Title: "Ticket", State: "ready", Digest: child}})
	if head := strings.Join(g.m.header(80), "\n"); !strings.Contains(head, "task of GATE-49") {
		t.Errorf("the header does not name the task's issue:\n%s", head)
	}
}

// TestTheDigestsWordsWinOverTheStatus: where the status read of a ticket says a
// phase otherwise, the digest's words stand and the sub-steps stay.
func TestTheDigestsWordsWinOverTheStatus(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	tk := g.m.w.tickets["JIG-1"]
	if tk.status == nil {
		t.Fatal("the status was not read")
	}
	d := task("build", false, "done|a brief the roster worded", "current|a build the roster worded", "pending|a", "pending|b", "pending|c")
	g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Greeting copy", State: "ready", Digest: d}})
	rows := g.m.steps(tk)
	if rows[0].Text != "a brief the roster worded" || rows[1].Text != "a build the roster worded" {
		t.Errorf("the steps say %q and %q, want the digest's words", rows[0].Text, rows[1].Text)
	}
	if subs := g.m.phaseOf(tk).Phases[1].Subs; len(subs) == 0 {
		t.Errorf("the build lost its sub-steps")
	}
	if st, _ := g.m.Standing("JIG-1"); st.Ready {
		t.Errorf("ready is true, the digest says it is not")
	}
}

// TestNoFrameGuessesBeforeTheRosterIsRead: before the roster arrives main says
// it is reading the store, not that it holds no tickets, and a ticket whose
// lines arrived but which the roster has not listed says reading for its steps
// and time, not sections and a build.
func TestNoFrameGuessesBeforeTheRosterIsRead(t *testing.T) {
	t.Parallel()
	cfg := Config{Run: func([]string) (string, int) { return "", 0 }, Zone: time.UTC, Width: 80, Height: 24, Project: "demo",
		Now: func() time.Time { return rigNow }}

	m := New(cfg)
	main := pagesText(m.mainPage())
	if !strings.Contains(main, "reading the store…") || strings.Contains(main, "no tickets yet") || strings.Contains(main, "0 tickets") {
		t.Errorf("main before the roster:\n%s", main)
	}
	if head := strings.Join(m.header(80), "\n"); strings.Contains(head, "0 tickets") && !strings.Contains(main, "reading") {
		t.Errorf("the header counts tickets before there is a roster:\n%s", head)
	}

	// The stream shows a ticket the roster has not listed.
	m.w.apply([]events.Envelope(jig1()))
	tk := m.w.tickets["JIG-1"]
	for i, row := range m.steps(tk) {
		if row.Text != readingText || row.Took != "" || row.Done {
			t.Errorf("step %d of a ticket nothing was read of is %+v, want reading and no time", i+1, row)
		}
	}
	page := pagesText(m.ticketPage("JIG-1"))
	for _, guess := range []string{"0 sections", "being built", "total", "land "} {
		if strings.Contains(page, guess) {
			t.Errorf("the ticket screen before the roster says %q:\n%s", guess, page)
		}
	}
	if !strings.Contains(page, readingText) {
		t.Errorf("the ticket screen before the roster does not say reading:\n%s", page)
	}

	// Once the roster has been read, an empty store says so.
	m.w.setRoster(nil)
	m.w.tickets, m.w.order = map[string]*ticket{}, nil
	if main := pagesText(m.mainPage()); !strings.Contains(main, "no tickets yet") {
		t.Errorf("main of a store the roster listed no ticket of:\n%s", main)
	}
}

// TestARosterThatFailedKeepsTheLastDigestAndSaysWhy: a read of the roster that
// printed no ticket list says so in the notice, with the verb's words, and the
// words the last read gave stay.
func TestARosterThatFailedKeepsTheLastDigestAndSaysWhy(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	g.settle(g.m.Init())
	if got := g.m.w.tickets["JIG-2"].digest; got == nil || got.Current != "land" {
		t.Fatalf("the roster gave JIG-2 %+v", got)
	}
	g.r.answer("next --json --store /s", "error: no jig store found\ncode: ERROR\n", 1)
	g.settle(g.m.loadRoster())
	if !strings.Contains(g.m.notice, "no jig store found") {
		t.Errorf("the notice is %q, want the verb's words", g.m.notice)
	}
	if got := g.m.w.tickets["JIG-2"].digest; got == nil || got.Current != "land" {
		t.Errorf("the digest after a failed read is %+v, want the last one", got)
	}
}
