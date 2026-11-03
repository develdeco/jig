package e2e

// End-to-end test of the spend row of the chart screen of `jig tui`: a chart is
// graduated, its tickets get spend lines (a session's on one, a subagent's on
// the other), and the real binary on a pipe shows the run's spend in the Run
// panel, read through `jig next <chart> --json`. A chart with no spend lines
// shows no row.

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/spend"
	"github.com/develdeco/jig/internal/store"
)

// TestTUIChartScreenShowsItsSpend: the Run panel of a chart whose tickets have
// no spend lines has no spend row. Once a session has spent on the first ticket
// and a subagent of it on the second, the panel ends with a row of the tokens,
// the dollars, the tickets and the subagents.
func TestTUIChartScreenShowsItsSpend(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dir := joinPath(fx.StoreDir, "charts", "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, joinPath(dir, "map.md"), []byte(chartMapText))
	writeFileOrFatal(t, joinPath(dir, "tickets.yaml"), []byte("tickets:\n  - title: Apply a coupon code\n  - title: Show the discount\n"))
	if r := runJig(t, env, fx.RepoDir, "graduate", "checkout"); r.Code != 0 {
		t.Fatalf("jig graduate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("checkout")
	if err != nil || len(entries) != 2 || entries[0].ID == "" || entries[1].ID == "" {
		t.Fatalf("chart entries after graduate = %+v, %v, want both with a ticket", entries, err)
	}
	first, second := entries[0].ID, entries[1].ID

	// No spend line: the Run panel has no spend row.
	quiet := startJig(t, env, fx.RepoDir, "tui", "checkout")
	screens := tuiScreens{t: t, p: quiet}
	screen := screens.until("› checkout", "Run", "Tickets", first, second, "wave 1")
	if strings.Contains(screen, "spend ") || strings.Contains(screen, " tokens") {
		t.Errorf("the chart screen of a chart with no spend has a spend row:\n%s", screen)
	}
	screens.press("q")
	if res := quiet.Stop(); res.Code != 0 || res.Stderr != "" {
		t.Fatalf("jig tui checkout exit = %d, want 0 and no stderr\nstderr:\n%s", res.Code, res.Stderr)
	}

	// A session spent on the first ticket and its subagent on the second.
	model := []string{"claude-sonnet-5"}
	for id, line := range map[string]journal.Line{
		first:  spend.Line("", "s-run", "claude", "", spend.Totals{Input: 20_000, Output: 5_000, USD: 1.5, Models: model}),
		second: spend.AgentLine("", "s-run", "a-1", "claude", "", spend.Totals{Input: 5_000, USD: 0.25, Models: model}),
	} {
		if err := journal.Append(st, id, line); err != nil {
			t.Fatal(err)
		}
	}
	busy := startJig(t, env, fx.RepoDir, "tui", "checkout")
	screens = tuiScreens{t: t, p: busy}
	screen = screens.until("› checkout", "Run", "spend ", "30k tokens · $1.75 · 2 tickets · 1 subagent")
	if !strings.Contains(screen, "wave 1") {
		t.Errorf("the Run panel with spend has lost its waves:\n%s", screen)
	}
	screens.press("q")
	if res := busy.Stop(); res.Code != 0 || res.Stderr != "" {
		t.Fatalf("jig tui checkout exit = %d, want 0 and no stderr\nstderr:\n%s", res.Code, res.Stderr)
	}
}
