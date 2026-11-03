package e2e

// End-to-end test of the chart screen of `jig tui`: a chart is graduated, the
// real binary on a pipe lists it on the project's screen, opens its screen
// (the map, the tickets with what each waits for, the run, the activity), opens
// one of its tickets and comes back. The screens read the chart through
// `jig next --json` (the listing) and `jig next <chart> --json` (the chart
// screen's data): this test is written against those two verbs.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// chartMapText is the map.md of the chart the test graduates: the sections the
// screen shows.
const chartMapText = `# Coupons at checkout

## Destination

A shopper takes a coupon code from the cart to a discounted order.

## Decisions so far

- Codes are single use

## Not yet specified (fog)

- Stacking two codes

## Out of scope

- Gift cards
`

// TestTUIChartScreen: the project's screen lists the chart, enter opens its
// screen with the four panels, a ticket opens from it and esc comes back, and
// the command line opens it too.
func TestTUIChartScreen(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dir := joinPath(fx.StoreDir, "charts", "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, joinPath(dir, "map.md"), []byte(chartMapText))
	writeFileOrFatal(t, joinPath(dir, "tickets.yaml"), []byte(`tickets:
  - title: Apply a coupon code
  - title: Show the discount
    blocked_by:
      - ref: "#1"
        kind: merged
`))
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

	tui := startJig(t, env, fx.RepoDir, "tui")
	screens := tuiScreens{t: t, p: tui}

	// The project's screen lists the chart after the tickets; end puts the
	// cursor on its row and enter opens it.
	screens.until("charts", "checkout", "chart", "0 of 2 published")
	screens.press("\x1b[F") // end
	screens.press("\r")
	screen := screens.until("› checkout", "Map", "Tickets", "Run", "Activity", "waits for "+first+" merged", "ready")
	for _, want := range []string{
		"charts/checkout/map.md", "A shopper takes a coupon code from the cart to a discounted order.",
		"Codes are single use", "Stacking two codes", "Gift cards", "0 of 2 published", first, second,
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("the chart screen lacks %q:\n%s", want, screen)
		}
	}

	// A ticket opens from the chart and esc comes back to it.
	screens.press("\r")
	screens.until("› "+first, "Steps")
	screens.press("\x7f")
	screens.until("› checkout", "Tickets", "Run", "waits for "+first+" merged")

	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	} else if res.Stderr != "" {
		t.Errorf("jig tui wrote to stderr under its screens:\n%s", res.Stderr)
	}

	// The command line opens the chart's screen with the project one step back.
	again := startJig(t, env, fx.RepoDir, "tui", "checkout")
	screens = tuiScreens{t: t, p: again}
	screens.until("› checkout", "Tickets", "waits for "+first+" merged")
	screens.press("\x1b")
	screens.until("charts", "0 of 2 published")
	screens.press("q")
	if res := again.Stop(); res.Code != 0 || res.Stderr != "" {
		t.Fatalf("jig tui checkout exit = %d, want 0 and no stderr\nstderr:\n%s", res.Code, res.Stderr)
	}
}

// TestTUIChartActivityShowsTheChartsStoreHistory: a chart of one entry is
// graduated and its map is edited by a commit in the store clone. `jig tui
// <chart>` on a pipe shows the edit and the graduation as lines of the
// Activity panel (the chart has no journal, so the commits are its activity). An
// edit committed while the screen is open shows once r reads the chart again.
func TestTUIChartActivityShowsTheChartsStoreHistory(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dir := joinPath(fx.StoreDir, "charts", "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, joinPath(dir, "map.md"), []byte(chartMapText))
	writeFileOrFatal(t, joinPath(dir, "tickets.yaml"), []byte("tickets:\n  - title: Apply a coupon code\n"))
	if r := runJig(t, env, fx.RepoDir, "graduate", "checkout"); r.Code != 0 {
		t.Fatalf("jig graduate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("checkout")
	if err != nil || len(entries) != 1 || entries[0].ID == "" {
		t.Fatalf("chart entries after graduate = %+v, %v, want one with a ticket", entries, err)
	}
	id := entries[0].ID

	mapPath := joinPath(dir, "map.md")
	writeFileOrFatal(t, mapPath, []byte(strings.Replace(chartMapText, "- Codes are single use", "- Codes are single use\n- A code names its shop", 1)))
	commitStorePath(t, fx.StoreDir, "charts/checkout/map.md", "checkout: a code names its shop")

	tui := startJig(t, env, fx.RepoDir, "tui", "checkout")
	screens := tuiScreens{t: t, p: tui}
	screens.until("› checkout", "Activity", "checkout: a code names its shop", "chart checkout: graduate "+id, "chart store")

	// An edit made while the screen is open is read with the chart again, on r.
	writeFileOrFatal(t, mapPath, []byte(strings.Replace(chartMapText, "- Codes are single use", "- Codes are single use\n- A code has an expiry", 1)))
	commitStorePath(t, fx.StoreDir, "charts/checkout/map.md", "checkout: a code has an expiry")
	screens.press("r")
	screens.until("Activity", "checkout: a code has an expiry", "checkout: a code names its shop")

	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	} else if res.Stderr != "" {
		t.Errorf("jig tui wrote to stderr under its screens:\n%s", res.Stderr)
	}
}

// detailPanelOf is the lines of the Detail panel of a screen, from its top edge
// to its bottom one, "" when the screen has none.
func detailPanelOf(screen string) string {
	var panel []string
	for _, line := range strings.Split(screen, "\n") {
		switch {
		case strings.Contains(line, "╭─ Detail"):
			panel = []string{line}
		case panel != nil && strings.Contains(line, "╰"):
			return strings.Join(append(panel, line), "\n")
		case panel != nil:
			panel = append(panel, line)
		}
	}
	return ""
}

// TestTUIChartScreenDetailsTheTicketUnderTheCursor: a subagent claims the
// chart's ticket and commits, its verification runs, and its stop is held. The
// real binary on a pipe, with the cursor on that ticket, shows on the chart
// screen the slice, its stage, the session that holds it, the agent that
// claimed it and the hold, and the hold is gone once the verification lands.
// It is the hook chain of TestSubagentStopHoldsOnlyTheClaimHolder, watched from
// the chart screen.
func TestTUIChartScreenDetailsTheTicketUnderTheCursor(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	// A session id in the caller's environment would name a second builder: the
	// run is the harness's session alone.
	env = env.withGitGlobal(t).without("JIG_SESSION", "JIG_SLICE")
	main := newHarness(t, fx, env, "s-chart")
	sub := newHarness(t, fx, env, "s-chart")
	sub.Agent = "a-1"
	main.Branch(fx.Branch)

	if res := runJig(t, env, fx.RepoDir, "hooks", "--global", "--harness", "claude", "--yes"); res.Code != 0 {
		t.Fatalf("jig hooks --global exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if res := runJig(t, env, fx.RepoDir, "githook", "--global"); res.Code != 0 {
		t.Fatalf("jig githook --global exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	subagentStop := installedHookCommand(t, claudeSettings(env), "SubagentStop")

	// An unattended claim needs the brief approved.
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := st.BriefSHA256(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	approval := store.Approval{By: "Test Person", At: time.Now().UTC().Format(time.RFC3339), BriefSHA256: sum, Provenance: "person"}
	if err := st.WriteApproval(fx.Ticket, approval); err != nil {
		t.Fatal(err)
	}

	// The chart is the fixture's one ticket.
	dir := joinPath(fx.StoreDir, "charts", "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, joinPath(dir, "map.md"), []byte(chartMapText))
	writeFileOrFatal(t, joinPath(dir, "tickets.yaml"), []byte("tickets:\n  - id: "+fx.Ticket+"\n    title: Greet the person\n"))

	// The subagent claims slice a unattended, commits it while its verification
	// is held, and is held back from stopping.
	if res := jigHooked(sub, "claim", fx.Ticket, "--slice", "a", "--unattended"); res.Code != 0 {
		t.Fatalf("jig claim --unattended exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	release := sub.HoldVerifications("alpha", "beta")
	sha := sub.CommitScenario("a", 1)
	waitFor(t, 10*time.Second, "the worker git's hook started", func() bool { return spawnedFor(readLedger(t, env), sha) == 1 })
	stop := runHookCommand(t, env, fx.RepoDir, subagentStop, sub.Payload("SubagentStop", map[string]any{"stop_hook_active": false}))
	if held := decodeHook(t, stop); held.Decision != "block" {
		t.Fatalf("SubagentStop = %+v, want a refusal while the verification runs", held)
	}

	// The chart screen, with the cursor on the chart's only ticket, details it.
	tui := startJig(t, env, fx.RepoDir, "tui", "checkout")
	screens := tuiScreens{t: t, p: tui}
	screen := screens.until("› checkout", "╭─ Detail", "╭─ Run", "a · verifying · builder · agent a-1", "agent a-1 held at its stop", "verification of "+sha[:12])
	if edge := detailPanelOf(screen); !strings.HasSuffix(strings.SplitN(edge, "\n", 2)[0], fx.Ticket+" ─╮") {
		t.Errorf("the Detail panel's top edge does not end with the ticket %s:\n%s", fx.Ticket, screen)
	}
	if tickets, detail, run := strings.Index(screen, "╭─ Tickets"), strings.Index(screen, "╭─ Detail"), strings.Index(screen, "╭─ Run"); !(tickets < detail && detail < run) {
		t.Errorf("the Detail panel is not between the Tickets and the Run panels:\n%s", screen)
	}

	// The verification lands: the hold is over, so the Detail panel no longer
	// says it (the Activity panel keeps the history of it).
	release()
	waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventVerified && l.Commit == sha && l.Slice == "a"
	})
	sub.WaitVerifiers()
	if screen = screens.until("1 of 4 green"); strings.Contains(detailPanelOf(screen), "held at its stop") {
		t.Errorf("the Detail panel still says the stop is held once the verification landed:\n%s", screen)
	}

	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	} else if res.Stderr != "" {
		t.Errorf("jig tui wrote to stderr under its screens:\n%s", res.Stderr)
	}
}
