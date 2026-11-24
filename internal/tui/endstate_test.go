package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/develdeco/jig/internal/journal"
)

// stateRows are the digests of a ticket at each of the four end states.
func endDigests() map[string]*digestInfo {
	const (
		target = "501-review-rounds-stop-repeating-work"
		url    = "https://example.test/acme/shop/pull/503"
	)
	pr := digestPR{Repo: "shop", Number: 503, URL: url, State: "open", Target: target}
	work := func(publish, land string) *digestInfo {
		d := task("land", false, "done|approved by Ada", "done|1 slice green", "done|round 1 clean", publish, land)
		d.PRs = []digestPR{pr}
		return d
	}
	behind := work("done|pull request opened · "+url, "current|waits for a merge")
	behind.LandFirst = []string{"JIG-3"}
	waits := work("done|pull request opened · "+url, "current|waits for a merge")
	landed := work("done|pull request opened · "+url, "done|landed")
	closed := task("brief", false, "current|drafted · waiting for your approval", "pending|after the brief", "pending|after a clean review", "done|closed · not planned", "done|none · closed without a pull request")
	closed.Closed = "not planned"
	return map[string]*digestInfo{StateOnLand: behind, StateWaitsToLand: waits, StateLanded: landed, StateClosed: closed}
}

// TestEndStatesReadApart: a ticket published behind one that has not landed, one
// that waits to land, one landed and one closed read as four different states,
// each with its own icon and words, and main's row, the chart's row and Detail
// and the ticket's header say the same one.
func TestEndStatesReadApart(t *testing.T) {
	t.Parallel()
	words := map[string]string{
		StateOnLand:      "published · PR #503 open into 501-review-rounds-stop-repeating-work · lands after JIG-3",
		StateWaitsToLand: `waits to land · say "Land JIG-4"`,
		StateLanded:      "landed · into main at 09:15:30",
		StateClosed:      "closed · not planned",
	}
	header := map[string]string{StateOnLand: "published", StateWaitsToLand: "waits to land", StateLanded: "landed", StateClosed: "closed"}
	icons := map[string]string{}

	for state, d := range endDigests() {
		g := chartRig(t)
		// Wide enough that the branch the pull request goes into is one word on
		// one row.
		g.send(tea.WindowSizeMsg{Width: 140, Height: 30})
		g.feed(jl(at(15, 30), "JIG-4", "", journal.EventLanded, func(l *journal.Line) { l.Base = "main" }))
		tk := g.m.w.tickets["JIG-4"]
		if state != StateLanded {
			tk.lines = tk.lines[:len(tk.lines)-1]
		}
		tk.digest = d
		icon := lookOf(state).Icon
		if prev, dup := icons[icon]; dup {
			t.Errorf("%s and %s share the icon %s", prev, state, icon)
		}
		icons[icon] = state

		// The project's list.
		row := lineWith(pagesText(g.m.mainPage()), "JIG-4")
		if !strings.Contains(row, icon+" JIG-4") || !strings.Contains(row, words[state]) {
			t.Errorf("%s: main's row is not %s with %q:\n%s", state, icon, words[state], row)
		}
		// The chart's row and its Detail.
		c := g.m.w.charts["checkout"]
		cr := g.m.chartRows(c)[0]
		if note := g.m.chartNote(cr); cr.state() != state || !strings.Contains(note, words[state]) {
			t.Errorf("%s: the chart's row is %s saying %q, want %q", state, cr.state(), note, words[state])
		}
		if got := chartStateWord(cr); got != header[state] {
			t.Errorf("%s: the Detail says %q, want %q", state, got, header[state])
		}
		// The ticket's header.
		if st, label := g.m.stepLabel(tk); st != state || label != header[state] {
			t.Errorf("%s: the header says %s %q, want %q", state, st, label, header[state])
		}
	}
	if len(icons) != 4 {
		t.Errorf("the four states have %d icons: %v", len(icons), icons)
	}
}

// TestClosedTicketReadsClosedEverywhere: a ticket closed before its brief was
// approved, and one closed with a round open, read closed in every step they
// never finished, in the header, on main and on the chart, never "reading", "ready
// for a fresh session" or a count of green slices; a step they finished keeps
// its words.
func TestClosedTicketReadsClosedEverywhere(t *testing.T) {
	t.Parallel()
	unapproved := task("brief", false, "current|drafted · waiting for your approval", "pending|0 of 1 green", "pending|ready for a fresh session to review the change", "done|closed · not planned", "done|none · closed without a pull request")
	roundOpen := task("review", false, "done|approved by Ada", "done|1 slice green", "current|round 1 open", "done|closed · not planned", "done|none · closed without a pull request")
	for name, d := range map[string]*digestInfo{"a brief never approved": unapproved, "a round open": roundOpen} {
		d.Closed = "not planned"
		g := chartRig(t)
		g.m.w.tickets["JIG-4"].digest = d
		tk := g.m.w.tickets["JIG-4"]

		steps := g.m.steps(tk)
		for i, s := range steps {
			switch {
			case s.Done && s.Text != d.Phases[i].Text:
				t.Errorf("%s: the finished step %d says %q, the digest %q", name, i+1, s.Text, d.Phases[i].Text)
			case !s.Done && (s.State != StateClosed || s.Text != "closed"):
				t.Errorf("%s: the unfinished step %d is %s %q, want closed", name, i+1, s.State, s.Text)
			}
		}
		if st, label := g.m.stepLabel(tk); st != StateClosed || label != "closed" {
			t.Errorf("%s: the header says %s %q", name, st, label)
		}
		for screen, text := range map[string]string{
			"the ticket": pagesText(g.m.ticketPage("JIG-4")),
			"main":       lineWith(pagesText(g.m.mainPage()), "JIG-4"),
			"the chart":  lineWith(pagesText(g.m.chartPage("checkout")), "JIG-4"),
		} {
			for _, bad := range []string{"reading", "ready for a fresh session", "of 1 green", "being drafted", "round 1 open", "waiting for your approval"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s: %s says %q for a closed ticket:\n%s", name, screen, bad, text)
				}
			}
			if !strings.Contains(text, "closed") || !strings.Contains(text, "⊖") {
				t.Errorf("%s: %s does not read closed with its icon:\n%s", name, screen, text)
			}
		}
		if !strings.Contains(lineWith(pagesText(g.m.mainPage()), "JIG-4"), "closed · not planned") {
			t.Errorf("%s: main does not give the reason", name)
		}
	}

	// A question nobody answered does not keep a closed ticket open on main.
	g := newRig(t, "")
	g.feed(jig1()...)
	d := task("brief", false, "current|drafted", "pending|a", "pending|b", "done|closed · not planned", "done|none · closed without a pull request")
	d.Closed = "not planned"
	g.pin("JIG-1", d)
	g.syncDigests()
	if row := lineWith(pagesText(g.m.mainPage()), "JIG-1"); strings.Contains(row, "open question") || !strings.Contains(row, "closed · not planned") {
		t.Errorf("a closed ticket with a question open reads:\n%s", row)
	}
}

// TestLandedLinesAreTheLatestOfEachRepoAndContainer: a ticket that landed in two
// repos has a landing for each, and a landing written again stands for its repo.
func TestLandedLinesAreTheLatestOfEachRepoAndContainer(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	land := func(ts, repo, base string) {
		g.feed(jl(ts, "JIG-4", "", journal.EventLanded, func(l *journal.Line) { l.Repo, l.Base = repo, base }))
	}
	land(at(15, 0), "api", "main")
	land(at(15, 10), "web", "main")
	land(at(15, 20), "api", "release")
	got := g.m.landedLines(g.m.w.tickets["JIG-4"])
	when := func(sec int) time.Time { return time.Date(2026, 10, 23, 9, 15, sec, 0, time.UTC) }
	want := []landedAt{{Repo: "api", Into: "release", At: "09:15:20", When: when(20)}, {Repo: "web", Into: "main", At: "09:15:10", When: when(10)}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("landed lines = %+v, want %+v", got, want)
	}
}

// TestTheRosterReadsWhatMustLandFirst: the digest's land_first reaches the
// screens from `jig next --json`.
func TestTheRosterReadsWhatMustLandFirst(t *testing.T) {
	t.Parallel()
	rows, _, err := parseRoster(`{"tickets":[{"id":"JIG-4","title":"T","state":"done","digest":{"kind":"task","land_first":["JIG-3","JIG-2"],"phases":[]}}]}` + "\n")
	if err != nil || len(rows) != 1 || rows[0].Digest == nil || strings.Join(rows[0].Digest.LandFirst, ",") != "JIG-3,JIG-2" {
		t.Errorf("rows = %+v, %v, want land_first JIG-3, JIG-2", rows, err)
	}
}

// TestEndStateOfNamesTargetsAndUnknownFacts: a ticket of several repos names each
// pull request and each landing, a shared task lands into each issue, a pull
// request the digest knows nothing of says so and is never open, and one with no
// number shows its URL.
func TestEndStateOfNamesTargetsAndUnknownFacts(t *testing.T) {
	t.Parallel()
	publish := "done|pull request opened"
	repos := task("land", false, "done|a", "done|b", "done|c", publish, "current|waits for a merge")
	repos.PRs = []digestPR{
		{Repo: "api", Number: 7, URL: "https://x/7", State: "open", Target: "main"},
		{Repo: "web", Number: 3, URL: "https://x/3", State: "unknown", Target: "main"},
		{URL: "https://git.example/review/9", State: "merged", Target: "release"},
	}
	repos.LandFirst = []string{"JIG-1", "JIG-2"}
	got, ok := endStateOf("JIG-9", repos, nil)
	want := "published · PR #7 open into main, PR #3 into main (state not read), https://git.example/review/9 merged · lands after JIG-1, JIG-2"
	if !ok || got.State != StateOnLand || got.Words != want {
		t.Errorf("published in three repos = %+v, want %q", got, want)
	}
	if strings.Contains(got.Words, "PR #3 open") {
		t.Errorf("a pull request with no fact is said open: %s", got.Words)
	}

	// A merge the host made and nobody recorded waits for the person's word even
	// behind another ticket: the order is the landing's, and the fact is made.
	merged := task("land", false, "done|a", "done|b", "done|c", publish, "current|merged on the host, not recorded")
	merged.LandFirst = []string{"JIG-1"}
	if got, _ := endStateOf("JIG-9", merged, nil); got.State != StateWaitsToLand || got.Words != `merged on the host, not recorded · say "Land JIG-9"` {
		t.Errorf("merged on the host = %+v", got)
	}

	// A pull request closed on the host says so.
	closedPR := task("land", false, "done|a", "done|b", "done|c", publish, "current|waits for a merge")
	closedPR.LandFirst = []string{"JIG-1"}
	closedPR.PRs = []digestPR{{Repo: "api", Number: 5, URL: "https://x/5", State: "closed", Target: "main"}}
	if got, _ := endStateOf("JIG-9", closedPR, nil); !strings.Contains(got.Words, "PR #5 closed into main") {
		t.Errorf("a closed pull request reads %q", got.Words)
	}

	// A ticket of one repo does not name it; a shared task lands into each issue.
	landed := task("land", false, "done|a", "done|b", "done|c", publish, "done|landed")
	got, _ = endStateOf("JIG-9", landed, []landedAt{{Into: "501-first-issue", At: "16:14:51"}, {Into: "502-second-issue", At: "16:20:02"}})
	if got.State != StateLanded || got.Words != "landed · into 501-first-issue at 16:14:51; into 502-second-issue at 16:20:02" {
		t.Errorf("landed into two issues = %+v", got)
	}
	// And a ticket of several repos names each repo's landing.
	got, _ = endStateOf("JIG-9", landed, []landedAt{{Repo: "api", Into: "main", At: "16:14:51"}, {Repo: "web", Into: "main", At: "16:15:00"}})
	if got.Words != "landed · api into main at 16:14:51; web into main at 16:15:00" {
		t.Errorf("landed in two repos = %q", got.Words)
	}
	got, _ = endStateOf("JIG-9", landed, []landedAt{{Repo: "api", Into: "main", At: "16:14:51"}})
	if got.Words != "landed · into main at 16:14:51" {
		t.Errorf("landed in the one repo the ticket has = %q", got.Words)
	}
	// Nothing to merge: the phase's own words.
	none := task("land", false, "done|a", "done|b", "done|c", "done|published", "done|none · no pull request to merge")
	if got, _ := endStateOf("JIG-9", none, nil); got.State != StateLanded || got.Words != "none · no pull request to merge" {
		t.Errorf("no pull request to merge = %+v", got)
	}

	// Not at an end: unpublished, behind (its publish current again), unlisted.
	for name, d := range map[string]*digestInfo{
		"unpublished": task("review", true, "done|a", "done|b", "current|round 1 open", "pending|c", "pending|d"),
		"behind":      task("publish", true, "done|a", "done|b", "done|c", "current|published - behind", "pending|d"),
		"unlisted":    nil,
	} {
		if got, ok := endStateOf("JIG-9", d, nil); ok {
			t.Errorf("%s stands at the end state %+v", name, got)
		}
	}
}
