package e2e

// End-to-end test of the merge timer of `jig tui`: a chart whose run ends at a
// merge is read again on its own, so the screen shows the merge with no key
// pressed. A merge on the host writes no line to any journal; the test makes
// one the way a host does, on a bare repo on disk, and fetches it into the
// clone, since nothing in jig fetches.

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// TestTUIChartSeesAMergeWithoutR: the chart screen of a chart whose second
// ticket waits for the first to merge says so; once the first ticket's branch
// is merged on origin and the clone has fetched it, the screen reads the second
// ticket as ready, with no key pressed, because the screen reads the chart
// again every --merge-every.
func TestTUIChartSeesAMergeWithoutR(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
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
	briefed(t, st, first)
	briefed(t, st, second)
	publishedWith(t, st, first)

	// The first ticket's branch is on origin, not merged.
	branch := nextJSON[nextTicketOut](t, env, fx.RepoDir, first).Branch
	if branch == "" {
		t.Fatalf("jig next %s --json names no branch", first)
	}
	gitLog(t, fx.RepoDir, "switch", "-c", branch, "main")
	writeFileOrFatal(t, joinPath(fx.RepoDir, "discount.txt"), []byte("the first ticket's work\n"))
	gitLog(t, fx.RepoDir, "add", "-A")
	commitPinned(t, fx.RepoDir, "the first ticket's work")
	gitLog(t, fx.RepoDir, "push", "origin", branch)
	gitLog(t, fx.RepoDir, "switch", "main")
	gitLog(t, fx.RepoDir, "fetch", "origin")

	tui := startJig(t, env, fx.RepoDir, "tui", "checkout", "--merge-every", "1s")
	screens := tuiScreens{t: t, p: tui}
	screens.until("› checkout", "Tickets", "waits for "+first+" merged", "waits for a merge")

	// The host merges the branch into main. The jig clone is not involved: a
	// scratch clone of origin does it and pushes, and the clone then fetches,
	// as a person's git fetch or a pull would.
	scratch := t.TempDir()
	if _, err := gitx.Run(scratch, "clone", fx.RepoRemote, "."); err != nil {
		t.Fatalf("clone of origin: %v", err)
	}
	if _, err := gitx.RunEnv(scratch, pinnedIdentityEnv, "merge", "--no-ff", "-m", "merge "+branch, "origin/"+branch); err != nil {
		t.Fatalf("merge of %s on origin: %v", branch, err)
	}
	gitLog(t, scratch, "push", "origin", "main")
	gitLog(t, fx.RepoDir, "fetch", "origin")

	// No key is pressed: the timer reads the chart and the screen shows the merge.
	screen := screens.until("› checkout", "Tickets", "ready · build next")
	if strings.Contains(screen, "waits for "+first+" merged") {
		t.Errorf("the chart screen still waits for %s after its merge:\n%s", first, screen)
	}
	if strings.Contains(screen, second+" waits for") {
		t.Errorf("the run still ends at %s after the merge:\n%s", second, screen)
	}

	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	} else if res.Stderr != "" {
		t.Errorf("jig tui wrote to stderr under its screens:\n%s", res.Stderr)
	}
}
