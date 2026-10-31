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

	"github.com/develdeco/jig/internal/fixture"
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
