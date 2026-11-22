package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestChartNewItemRefusals drives the refusals of `jig chart new --item` with
// the real binary: every one comes before any read of the tracker, so none
// needs a token or a network, and none mints a ticket or makes a chart.
func TestChartNewItemRefusals(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.Tickets()

	refuse := func(want string, args ...string) {
		t.Helper()
		r := runJig(t, env, fx.StoreDir, append([]string{"chart", "new"}, args...)...)
		if r.Code == 0 || !strings.Contains(r.Stdout, want) {
			t.Errorf("jig chart new %s: exit %d, want a refusal with %q\n%s", strings.Join(args, " "), r.Code, want, r.Stdout)
		}
	}

	// A project with no tracker.
	refuse("trackers:", "shop", "--item", "48")

	path := filepath.Join(fx.StoreDir, "project.yaml")
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "trackers: []") {
		t.Fatalf("test setup: %s, %v", body, err)
	}
	tracked := strings.Replace(string(body), "trackers: []", "trackers:\n  - github:\n      repo: example/tracking\n      project: https://github.com/users/example/projects/7", 1)
	if err := os.WriteFile(path, []byte(tracked), 0o644); err != nil {
		t.Fatal(err)
	}

	refuse("owner/repo#48", "shop", "--item", "forty-eight")
	refuse("owner/repo#48", "shop", "--item", "https://github.com/example/tracking/pull/48")
	refuse("example/tracking", "shop", "--item", "other/repo#48")
	refuse("excludes", "shop", "--item", "48", "--need", "n")

	if ids, _ := st.Tickets(); len(ids) != len(before) {
		t.Errorf("a refusal minted: tickets %v, were %v", ids, before)
	}
	if names, _ := st.ChartNames(); len(names) != 0 {
		t.Errorf("a refusal made charts %v", names)
	}
}
