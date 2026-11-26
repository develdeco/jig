package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// TestLandIsTheFifthStep: a ticket's screen has five rows, the fifth Land, at
// each state the digest can give it, and the standing the screens report has
// five states equal to the digest's phases.
func TestLandIsTheFifthStep(t *testing.T) {
	t.Parallel()
	const prURL = "https://example.test/acme/demo/pull/503"
	work := func(land string, publish string) *digestInfo {
		return task("land", false, "done|approved by Ada", "done|2 slices green", "done|round 1 clean", publish, land)
	}
	pr := digestPR{Repo: "demo", Number: 503, URL: prURL, State: "open", Target: "main"}
	unpublished := task("review", true, "done|approved by Ada", "done|2 slices green", "current|round 1 open", "pending|after a clean review", "pending|after the publish")
	merged := work("current|merged on the host, not recorded", "done|pull request opened · "+prURL)
	merged.PRs = []digestPR{pr}
	waits := work("current|waits for a merge", "done|pull request opened · "+prURL)
	waits.PRs = []digestPR{pr}
	landed := work("done|landed", "done|pull request opened · "+prURL)
	landed.PRs = []digestPR{pr}
	none := work("done|none · no pull request to merge", "done|published")
	closed := task("publish", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
	closed.Closed = "not planned"

	for _, tc := range []struct {
		name  string
		d     *digestInfo
		words string
		icon  string
	}{
		{"before the publish", unpublished, "after the publish", "○"},
		{"waiting for the person's word", waits, "waits for a merge", "◇"},
		{"merged on the host and not recorded", merged, "merged on the host, not recorded", "◇"},
		{"landed", landed, "landed", "✓"},
		{"nothing to merge", none, "none · no pull request to merge", "✓"},
		{"closed", closed, "none · closed without a pull request", "⊖"},
	} {
		g := newRig(t, "")
		g.m.w.setRoster([]rosterRow{{ID: "JIG-9", Title: "Ticket", State: "ready", Digest: tc.d}})

		page := pagesText(g.m.ticketPage("JIG-9"))
		for i, name := range []string{"1 Brief", "2 Build", "3 Review", "4 Publish", "5 Land"} {
			if !strings.Contains(page, name) {
				t.Errorf("%s: the Steps panel has no row %d (%s):\n%s", tc.name, i+1, name, page)
			}
		}
		if want := tc.icon + " 5 Land"; !strings.Contains(page, want) || !strings.Contains(page, tc.words) {
			t.Errorf("%s: the Land row is not %q saying %q:\n%s", tc.name, want, tc.words, page)
		}
		dots := g.m.stepDots(g.m.steps(g.m.w.tickets["JIG-9"]))
		if row := g.mainText(); len(strings.Fields(dots)) != 5 || !strings.Contains(row, dots) {
			t.Errorf("%s: the main row does not draw the five dots %q:\n%s", tc.name, dots, row)
		}

		st, ok := g.m.Standing("JIG-9")
		if !ok || len(st.States) != 5 || st.Current != tc.d.Current {
			t.Fatalf("%s: standing %+v, want five states at %s", tc.name, st, tc.d.Current)
		}
		for i, p := range tc.d.Phases {
			if st.States[i] != p.State {
				t.Errorf("%s: standing has %s %s, the digest %s", tc.name, p.Name, st.States[i], p.State)
			}
		}
	}
}

// TestLandedLineIsFiledUnderLand: a landed line, and the ledger's line of a
// merge named for the session to make, are lines of the land step: listed in
// its activity, tagged land and not build, and the step's time is the gap
// between its own lines.
func TestLandedLineIsFiledUnderLand(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 40})
	g.feed(jig2()...)
	buildTook := g.m.steps(g.m.w.tickets["JIG-2"])[stepBuild].Took
	g.feed(
		ll(at(15, 0), "JIG-2", journal.EventLandNamed, func(l *journal.Line) { l.Session = "s-builder" }),
		jl(at(15, 30), "JIG-2", "", journal.EventLanded, func(l *journal.Line) {
			l.Session, l.URL, l.Base, l.Provenance = "s-builder", "https://example.test/acme/demo/pull/7", "main", journal.ProvenanceAgent
		}),
	)
	tk := g.m.w.tickets["JIG-2"]

	if got := len(tk.stepLines(stepLand)); got != 2 {
		t.Errorf("the land step has %d lines, want its two", got)
	}
	for _, l := range tk.stepLines(stepBuild) {
		if l.Event == journal.EventLanded || l.Event == journal.EventLandNamed {
			t.Errorf("the build step holds the %s line", l.Event)
		}
	}
	var tags []string
	for _, p := range g.m.placed(tk, g.m.phaseOf(tk)) {
		if p.l.Event == journal.EventLanded || p.l.Event == journal.EventLandNamed {
			tags = append(tags, tagOf(p.step, p.key))
		}
	}
	if len(tags) != 2 || tags[0] != "land·merge" || tags[1] != "land·record" {
		t.Errorf("the lines are tagged %v, want land·merge and land·record", tags)
	}

	steps := g.m.steps(tk)
	if got := steps[stepLand].Took; got != "30s" {
		t.Errorf("the land step took %q, want the 30s between its own lines", got)
	}
	if got := steps[stepBuild].Took; got != buildTook {
		t.Errorf("the build took %q once the landing was written, %q before", got, buildTook)
	}
	if !steps[stepLand].Done || steps[stepLand].Text != "landed" {
		t.Errorf("the land row = %+v, want landed", steps[stepLand])
	}

	if frame := g.view(); !strings.Contains(frame, "land·record") || !strings.Contains(frame, "landed into main") {
		t.Errorf("the activity does not tell the landing:\n%s", frame)
	}

	// The session that landed it holds a window of the land step, and the tag
	// is drawn in the colour of the publish.
	held := false
	for _, w := range g.m.moveWindows(tk, g.m.phaseOf(tk)) {
		held = held || w.step == stepLand
	}
	if !held {
		t.Errorf("no window of the land step: a call its holder made in it would be placed elsewhere")
	}
	g.m.st = styles{r: colored()}
	if frame := g.view(); !strings.Contains(frame, "\x1b[36mland·record") {
		t.Errorf("the land tag is not drawn in the colour of the publish:\n%s", frame)
	}
}

// TestPublishRowNamesItsPullRequestTarget: a published ticket's Publish row
// reads its pull requests by number and target, one for each repo, the url of
// one with no number, and the digest's own words for a ticket that records none.
func TestPublishRowNamesItsPullRequestTarget(t *testing.T) {
	t.Parallel()
	const target = "501-review-rounds-stop-repeating-work"
	opened := "done|pull request opened · https://example.test/acme/demo/pull/503"
	for _, tc := range []struct {
		name string
		prs  []digestPR
		want string
	}{
		{"one pull request", []digestPR{{Repo: "demo", Number: 503, URL: "https://x/503", State: "open", Target: target}}, "published · PR #503 open into " + target},
		{"two repos", []digestPR{
			{Repo: "api", Number: 7, URL: "https://x/7", State: "open", Target: "main"}, {Repo: "web", Number: 3, URL: "https://x/3", State: "merged", Target: "main"},
		}, "published · PR #7 open into main, PR #3 merged into main"},
		{"a pull request with no number", []digestPR{{URL: "https://git.example/review/9", State: "open", Target: "main"}}, "published · https://git.example/review/9 open"},
		{"none recorded", nil, "pull request opened · https://example.test/acme/demo/pull/503"},
	} {
		g := newRig(t, "")
		d := task("land", false, "done|approved by Ada", "done|2 slices green", "done|round 1 clean", opened, "current|waits for a merge")
		d.PRs = tc.prs
		g.m.w.setRoster([]rosterRow{{ID: "JIG-9", Title: "Ticket", State: "ready", Digest: d}})
		if got := g.m.steps(g.m.w.tickets["JIG-9"])[stepPublish]; got.Text != tc.want || got.State != StatePublished || !got.Done {
			t.Errorf("%s: the Publish row is %+v, want %q", tc.name, got, tc.want)
		}
	}

	// A closed ticket keeps the words of its publish, whatever pull requests it
	// recorded.
	g := newRig(t, "")
	d := task("publish", false, "done|closed", "done|closed", "done|closed", "done|closed · not planned", "done|none · closed without a pull request")
	d.PRs = []digestPR{{Repo: "demo", Number: 503, URL: "https://x/503", Target: target}}
	g.m.w.setRoster([]rosterRow{{ID: "JIG-9", Title: "Ticket", State: "ready", Digest: d}})
	if got := g.m.steps(g.m.w.tickets["JIG-9"])[stepPublish].Text; got != "closed · not planned" {
		t.Errorf("a closed ticket's Publish row says %q", got)
	}
}
