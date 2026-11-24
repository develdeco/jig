package tui

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// searchStore is a model of a store with an issue and its tasks (planned by a
// chart), a published task with a pull request, a waiting task, a closed one
// and a chart that plans no issue.
func searchStore(t testing.TB) *Model {
	t.Helper()
	m := New(quietConfig())
	m.width, m.height = 110, 40
	pending := []string{"pending|after the brief", "pending|after the build", "pending|after a clean review", "pending|after the publish"}
	published := task("land", false, "done|approved", "done|2 slices green", "done|round 1 clean", "done|pull request opened", "current|waits for a merge")
	published.PRs = []digestPR{{Repo: "demo", Number: 287, State: "open", Target: "main"}}
	waiting := task("build", false, "done|approved", "pending|after PUB-11", pending[1], pending[2], pending[3])
	waiting.LandFirst = []string{"PUB-11"}
	closed := task("publish", false, "done|closed", "done|closed", "done|closed", "done|closed", "done|none · closed without a pull request")
	closed.Closed = "covered elsewhere"
	t1 := task("build", true, "done|approved", "current|ready to build", pending[1], pending[2], pending[3])
	t1.Parent = "ISS-1"
	t2 := task("build", true, "done|approved", "current|ready to build", pending[1], pending[2], pending[3])
	t2.Parent = "ISS-1"
	m.w.setRoster([]rosterRow{
		{ID: "ISS-1", Title: "Rebuild the queue", Digest: &digestInfo{Kind: "issue", State: "waiting"}},
		{ID: "TSK-1", Title: "Sort the groups", Digest: t1},
		{ID: "TSK-2", Title: "Draw the headings", Digest: t2},
		{ID: "PUB-11", Title: "Publish the release notes", Digest: published},
		{ID: "WAI-3", Title: "Wait for the notes", Digest: waiting},
		{ID: "CLS-4", Title: "Drop the old screen", Digest: closed},
	})
	m.w.setCharts([]chartListing{
		{Name: "queue-plan", Title: "Queue plan", Tickets: []string{"TSK-1", "TSK-2"}, Issue: "ISS-1"},
		{Name: "tui-design", Title: "Design of the TUI", Tickets: []string{"WAI-3"}},
	})
	return m
}

// typed sends the keys of s one by one: runes as themselves.
func typed(m *Model, s string) {
	for _, r := range s {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func press(m *Model, t tea.KeyType) { _, _ = m.Update(tea.KeyMsg{Type: t}) }

// shown are the ids of the rows main draws, top to bottom.
func shown(m *Model) []string {
	var ids []string
	for _, r := range m.page().refs() {
		if r.kind == rowTicket || r.kind == rowChart {
			ids = append(ids, r.id)
		}
	}
	return ids
}

// found is what main draws after `/` and the query, then esc.
func found(m *Model, query string) string {
	typed(m, "/"+query)
	ids := shown(m)
	press(m, tea.KeyEsc)
	sort.Strings(ids)
	return strings.Join(ids, " ")
}

// TestSearchMatchesWordsByPrefixInAnyOrder: a row is kept when every term of the
// query starts a word of it, whatever the order: its id whole and by parts,
// the words of its title, its kind, its chart, its states and its pull
// requests' numbers, with or without the #.
func TestSearchMatchesWordsByPrefixInAnyOrder(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	for query, want := range map[string]string{
		"pub-11":        "PUB-11",
		"PUB":           "PUB-11",
		"11":            "PUB-11",
		"#287":          "PUB-11",
		"287":           "PUB-11",
		"28":            "PUB-11",
		"notes release": "PUB-11",
		"for notes":     "WAI-3",
		"no such":       "",
		// The kind: the issue's row, and the charts' rows by the word chart.
		"issue": "ISS-1",
		"chart": "tui-design",
		// An issue's tasks by the issue they are in, and by the chart's name.
		"iss-1":      "ISS-1 TSK-1 TSK-2",
		"queue-plan": "ISS-1 TSK-1 TSK-2",
		"queue":      "ISS-1 TSK-1 TSK-2",
		"tui-design": "WAI-3 tui-design",
		"design":     "WAI-3 tui-design",
		// A task's own words keep its issue and no other task.
		"draw":        "ISS-1 TSK-2",
		"sort groups": "ISS-1 TSK-1",
		// State words: the group's name and the end state's word.
		"waiting":   "WAI-3",
		"closed":    "CLS-4",
		"to land":   "PUB-11",
		"published": "",
	} {
		if got := found(m, query); got != want {
			t.Errorf("/%s draws %q, want %q", query, got, want)
		}
	}
	// The rule is the pure function's too.
	for _, c := range []struct {
		query string
		words []string
		want  bool
	}{
		{"", []string{"a"}, true},
		{"#12", []string{"a-12", "a", "12"}, true},
		{"2", []string{"a-12", "a", "12"}, false},
		{"AB cd", wordsOf("Cdx ab-1"), true},
		{"ab ab", []string{"ab"}, true},
		{"abc", []string{"ab"}, false},
	} {
		if got := matches(searchTerms(c.query), c.words); got != c.want {
			t.Errorf("matches(%q, %v) = %v, want %v", c.query, c.words, got, c.want)
		}
	}
}

// TestSearchKeepsTheIssueOfATaskHit: a hit on a task keeps its issue's row, and a
// hit on the issue keeps no task that does not match itself.
func TestSearchKeepsTheIssueOfATaskHit(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	typed(m, "/sort")
	frame := stripANSI(m.View())
	if ids := shown(m); strings.Join(ids, " ") != "ISS-1 TSK-1" {
		t.Errorf("/sort draws %v, want the issue and the task:\n%s", ids, frame)
	}
	if issue, task := strings.Index(frame, "ISS-1"), strings.Index(frame, "TSK-1"); issue < 0 || task < issue {
		t.Errorf("the task is not drawn under its issue:\n%s", frame)
	}
	press(m, tea.KeyEsc)
	// The issue alone: the id of the issue matches the tasks that name it as
	// their parent, but a word only of the issue's title keeps none of them.
	if got := found(m, "rebuild"); got != "ISS-1" {
		t.Errorf("/rebuild draws %q, want the issue alone", got)
	}
	if got := found(m, "issue"); got != "ISS-1" {
		t.Errorf("/issue draws %q, want the issues alone", got)
	}
}

// TestSearchUnfoldsFoldedGroups: a hit in a folded group is drawn with the group
// open, under its heading with the rows kept counted, and the fold is as it
// was after esc.
func TestSearchUnfoldsFoldedGroups(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	before := stripANSI(m.View())
	if !strings.Contains(before, "▸ done (1)") || strings.Contains(before, "CLS-4") {
		t.Fatalf("the closed ticket is not folded away before the search:\n%s", before)
	}
	typed(m, "/drop")
	frame := stripANSI(m.View())
	if !strings.Contains(frame, "done (1)") || !strings.Contains(frame, "CLS-4") || strings.Contains(frame, "▸ done") || strings.Contains(frame, "enter shows them") {
		t.Errorf("a hit in a folded group is not drawn under its open heading:\n%s", frame)
	}
	if sel := m.selected(); sel == nil || sel.id != "CLS-4" {
		t.Errorf("the cursor is on %+v, want the first match CLS-4", sel)
	}
	if m.nav[0].open["done"] {
		t.Errorf("the search opened the fold")
	}
	press(m, tea.KeyEsc)
	if after := stripANSI(m.View()); after != before {
		t.Errorf("esc left main changed:\n--- before\n%s\n--- after\n%s", before, after)
	}

	// The view by chart is narrowed the same way.
	typed(m, "s")
	byChart := stripANSI(m.View())
	typed(m, "/notes")
	frame = stripANSI(m.View())
	if strings.Contains(frame, "TSK-1") || !strings.Contains(frame, "PUB-11") || !strings.Contains(frame, "WAI-3") || !strings.Contains(frame, "tui-design") {
		t.Errorf("the view by chart is not narrowed to the matches and their charts:\n%s", frame)
	}
	press(m, tea.KeyEsc)
	if after := stripANSI(m.View()); after != byChart {
		t.Errorf("esc left the view by chart changed:\n--- before\n%s\n--- after\n%s", byChart, after)
	}
}

// TestSearchLineTakesEveryPrintableKey: the keys main acts on are text while the
// line is open, the line is shown with a caret and its hint, and the header
// counts the matches.
func TestSearchLineTakesEveryPrintableKey(t *testing.T) {
	t.Parallel()
	m := searchStore(t)
	typed(m, "/")
	frame := stripANSI(m.View())
	for _, want := range []string{"search · 7 rows", "/ ▏", "id, number, title, chart, state or PR · ↑↓ move · enter open · esc clears"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the open line does not say %q:\n%s", want, frame)
		}
	}
	typed(m, "q s ? / r t a")
	if m.quitting {
		t.Fatalf("q quit the program while the search line was open")
	}
	if got := string(m.nav[0].search.query); got != "q s ? / r t a" {
		t.Errorf("the line holds %q, want every key typed", got)
	}
	if m.nav[0].byChart || m.tail {
		t.Errorf("a typed s or t acted on main")
	}
	press(m, tea.KeyBackspace)
	if got := string(m.nav[0].search.query); got != "q s ? / r t " {
		t.Errorf("backspace left %q", got)
	}
	frame = stripANSI(m.View())
	if !strings.Contains(frame, "q s ? / r t ▏") {
		t.Errorf("the line does not show what was typed:\n%s", frame)
	}

	// The count is of the rows that match themselves, tickets once.
	press(m, tea.KeyEsc)
	typed(m, "/notes")
	head := stripANSI(m.header(110)[0])
	if want := "search “notes” · 2 of 7 · esc clears"; !strings.Contains(head, want) {
		t.Errorf("the header is %q, want it to say %q", head, want)
	}
	press(m, tea.KeyEsc)

	// Off the main screen the key is none.
	m.nav = append(m.nav, navEntry{screen: screenTicket, ticket: "PUB-11"})
	typed(m, "/")
	if m.nav[0].search.applied() {
		t.Errorf("/ opened a search on a ticket's screen")
	}
	if hint := stripANSI(strings.Join(m.footer(110), "\n")); strings.Contains(hint, "search") {
		t.Errorf("a ticket's screen lists the search key:\n%s", hint)
	}
}

// TestSearchReadsNothing: a key of the search asks the runner for nothing and
// builds neither the queue nor a row's detail again; a move of the store
// builds the queue once and the search is applied to it.
func TestSearchReadsNothing(t *testing.T) {
	t.Parallel()
	calls := 0
	cfg := quietConfig()
	cfg.Run = func([]string) (string, int) { calls++; return "", 0 }
	m := New(cfg)
	m.width, m.height = 110, 40
	src := searchStore(t)
	m.w.setRoster(rosterRows(src))
	m.w.setCharts(chartListings(src))
	m.View()
	calls, builds, clocks := 0, m.queueBuilds, m.clockRuns
	for _, k := range "/pub-1" {
		typed(m, string(k))
		m.View()
	}
	press(m, tea.KeyDown)
	press(m, tea.KeyBackspace)
	press(m, tea.KeyEsc)
	m.View()
	if calls != 0 {
		t.Errorf("the search asked the runner %d times", calls)
	}
	if m.queueBuilds != builds {
		t.Errorf("the search built the queue %d more times", m.queueBuilds-builds)
	}
	if m.clockRuns != clocks {
		t.Errorf("the search computed %d work clocks", m.clockRuns-clocks)
	}

	// A move of the store builds the queue again, once, whatever the keys.
	typed(m, "/notes")
	m.View()
	builds = m.queueBuilds
	m.w.gen++
	m.View()
	m.View()
	if m.queueBuilds != builds+1 {
		t.Errorf("a store move built the queue %d times, want once", m.queueBuilds-builds)
	}
	if ids := strings.Join(shown(m), " "); ids != "PUB-11 WAI-3" {
		t.Errorf("the search after a store move draws %q", ids)
	}
}

func rosterRows(m *Model) []rosterRow {
	var rows []rosterRow
	for _, id := range m.w.order {
		t := m.w.tickets[id]
		rows = append(rows, rosterRow{ID: id, Title: t.title, Digest: t.digest})
	}
	return rows
}

func chartListings(m *Model) []chartListing {
	var out []chartListing
	for _, name := range m.w.chartOrder {
		out = append(out, m.w.charts[name].listing)
	}
	return out
}

// searchBig is a store of n tickets with issues, tasks, charts and pull request
// numbers.
func searchBig(t testing.TB, n int) *Model {
	t.Helper()
	m := bigStore(t, n, 0, 0)
	var rows []rosterRow
	var charts []chartListing
	for i := 0; i < n; i++ {
		row := listed(fmt.Sprintf("T-%04d", i))
		switch {
		case i%25 == 0:
			row.Digest = &digestInfo{Kind: "issue", State: "waiting"}
			var ids []string
			for j := 1; j < 10 && i+j < n; j++ {
				ids = append(ids, fmt.Sprintf("T-%04d", i+j))
			}
			charts = append(charts, chartListing{Name: fmt.Sprintf("plan-%d", i), Title: "A plan", Tickets: ids, Issue: row.ID})
		case i%25 < 10:
			row.Digest.Parent = fmt.Sprintf("T-%04d", i-i%25)
		case i%7 == 0:
			row.Digest.PRs = []digestPR{{Repo: "demo", Number: 1000 + i, State: "open", Target: "main"}}
		}
		rows = append(rows, row)
	}
	for i := 0; i < n; i += 100 {
		charts = append(charts, chartListing{Name: fmt.Sprintf("chart-%d", i), Title: "A chart", Tickets: []string{fmt.Sprintf("T-%04d", i+50)}})
	}
	m.w.setRoster(rows)
	m.w.setCharts(charts)
	m.width, m.height = 110, 40
	return m
}

// TestSearchKeyRedrawsWithin50msOn1000Tickets: each key of a search, the line's
// first included, and esc, redraws within 50 ms from the key to the frame, the
// median of repeated runs.
func TestSearchKeyRedrawsWithin50msOn1000Tickets(t *testing.T) {
	// Not parallel: a key's time is measured, and other tests would load it.
	const budget = 50 * time.Millisecond
	median := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	keys := []struct {
		name string
		do   func(m *Model)
	}{
		{"the first key, /", func(m *Model) { typed(m, "/") }},
		{"a first letter", func(m *Model) { typed(m, "/t") }},
		{"a middle key", func(m *Model) { typed(m, "/t-00"); typed(m, "1") }},
		{"a number", func(m *Model) { typed(m, "/10") }},
		{"backspace", func(m *Model) { typed(m, "/t-001"); press(m, tea.KeyBackspace) }},
		{"esc", func(m *Model) { typed(m, "/t-001"); press(m, tea.KeyEsc) }},
	}
	m := searchBig(t, 1000)
	m.View()
	for _, k := range keys {
		var took []time.Duration
		for range 9 {
			// Each run starts from a main with no search, the queue built.
			press(m, tea.KeyEsc)
			m.View()
			m.main.srch = nil
			// Everything before the last key is set up; the last one is timed.
			var last func()
			switch k.name {
			case "the first key, /":
				last = func() { typed(m, "/") }
			case "a first letter":
				typed(m, "/")
				m.View()
				last = func() { typed(m, "t") }
			case "a middle key":
				typed(m, "/t-00")
				m.View()
				last = func() { typed(m, "1") }
			case "a number":
				typed(m, "/1")
				m.View()
				last = func() { typed(m, "0") }
			case "backspace":
				typed(m, "/t-001")
				m.View()
				last = func() { press(m, tea.KeyBackspace) }
			default:
				typed(m, "/t-001")
				m.View()
				last = func() { press(m, tea.KeyEsc) }
			}
			start := time.Now()
			last()
			m.View()
			took = append(took, time.Since(start))
		}
		got := median(took)
		t.Logf("%s: %v", k.name, got)
		if got > budget {
			t.Errorf("%s: key to frame takes %v on 1,000 tickets, want under %v", k.name, got, budget)
		}
	}
}
