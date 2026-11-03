package e2e

// End-to-end test of the chart's store history in `jig next <chart> --json`:
// the commits of the store clone that changed something under charts/<chart>/,
// newest first. A chart has no journal, so the chart screen reads this as its
// activity; the screen's own chain is in tui_chart_test.go.

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// commitStorePath commits the one path of the store clone with message, so no
// commit sweeps up another's file: add names the path, and the commit takes
// only what the index holds.
func commitStorePath(t *testing.T, storeDir, path, message string) {
	t.Helper()
	if _, err := gitx.Run(storeDir, "add", path); err != nil {
		t.Fatalf("git add %s: %v", path, err)
	}
	if _, err := gitx.Run(storeDir, "commit", "-m", message); err != nil {
		t.Fatalf("git commit %q: %v", message, err)
	}
}

// TestChartHistoryInNextJSON: a chart of two entries is graduated and its map
// is edited by a commit in the store clone; a commit that changed only another
// chart and one that changed only a ticket follow it. `jig next <chart> --json`
// then lists the commits of the chart's own folder newest first, the map edit
// first and both graduations after it, and neither of the others, with 40-hex
// shas and UTC times; the text form of the verb has no history.
func TestChartHistoryInNextJSON(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dir := joinPath(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, joinPath(dir, "map.md"), []byte(chartMapText))
	writeFileOrFatal(t, joinPath(dir, "tickets.yaml"), []byte("tickets:\n  - title: Apply a coupon code\n  - title: Show the discount\n"))
	if r := runJig(t, env, fx.RepoDir, "graduate", "c"); r.Code != 0 {
		t.Fatalf("jig graduate c: exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	c := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if len(c.Tickets) != 2 {
		t.Fatalf("the standing has %d tickets, want 2: %+v", len(c.Tickets), c.Tickets)
	}
	first, second := c.Tickets[0].ID, c.Tickets[1].ID

	// Three commits, each of one path: another chart's, a ticket's own, then
	// an edit of this chart's map.
	other := joinPath(fx.StoreDir, "charts", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, joinPath(other, "map.md"), []byte("# Another chart\n"))
	commitStorePath(t, fx.StoreDir, "charts/other/map.md", "other: a map of its own")
	writeFileOrFatal(t, joinPath(fx.StoreDir, "tickets", first, "scratch.md"), []byte("a file of the ticket\n"))
	commitStorePath(t, fx.StoreDir, "tickets/"+first+"/scratch.md", first+": a file of the ticket only")
	writeFileOrFatal(t, joinPath(dir, "map.md"), []byte(strings.Replace(chartMapText, "- Codes are single use", "- Codes are single use\n- A code names its shop", 1)))
	commitStorePath(t, fx.StoreDir, "charts/c/map.md", "c: a code names its shop")

	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	var subjects []string
	for _, h := range c.History {
		subjects = append(subjects, h.Subject)
	}
	if len(subjects) == 0 || subjects[0] != "c: a code names its shop" {
		t.Fatalf("history = %q, want the map edit first", subjects)
	}
	place := map[string]int{}
	for i, s := range subjects {
		place[s] = i
	}
	for _, id := range []string{first, second} {
		if _, ok := place["chart c: graduate "+id]; !ok {
			t.Errorf("history = %q, lacks %q", subjects, "chart c: graduate "+id)
		}
	}
	// The graduations are in the order they were made, the second one newer.
	if place["chart c: graduate "+second] > place["chart c: graduate "+first] {
		t.Errorf("history = %q, want the graduation of %s (made after %s) above it", subjects, second, first)
	}
	for _, s := range subjects {
		if strings.HasPrefix(s, "other:") || strings.Contains(s, "a file of the ticket only") {
			t.Errorf("history = %q, has %q, which changed nothing under charts/c/", subjects, s)
		}
	}

	hex40 := regexp.MustCompile(`^[0-9a-f]{40}$`)
	var newer time.Time
	for i, h := range c.History {
		if !hex40.MatchString(h.SHA) {
			t.Errorf("history[%d].sha = %q, want 40 hex", i, h.SHA)
		}
		at, err := time.Parse(time.RFC3339, h.Time)
		if err != nil || !strings.HasSuffix(h.Time, "Z") {
			t.Errorf("history[%d].time = %q (%v), want RFC 3339 in UTC, ending in Z", i, h.Time, err)
		}
		if i > 0 && at.After(newer) {
			t.Errorf("history[%d] (%s) is newer than history[%d] (%s): want newest first", i, h.Time, i-1, c.History[i-1].Time)
		}
		newer = at
	}
	if head, err := gitx.Run(fx.StoreDir, "rev-parse", "HEAD"); err != nil || c.History[0].SHA != head {
		t.Errorf("history[0].sha = %q, want the store's HEAD %q (%v): the map edit was the last commit", c.History[0].SHA, head, err)
	}

	if text := runJig(t, env, fx.RepoDir, "next", "c").Stdout; strings.Contains(text, "history") {
		t.Errorf("the text form of jig next c has a history, which is for --json only:\n%s", text)
	}
}
