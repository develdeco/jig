package e2e

// End-to-end tests of what a light rule in the manifest changes for `jig
// verify`: nothing. A slice's own oracle runs in full for a Markdown-only
// commit and for a delta with one non-light file alike.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

const e2eLightOracle = "echo lint-only"

// declareLightInClone adds the light rule to the clone's manifest, left in the
// working tree for the next commit to take in.
func declareLightInClone(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	path := filepath.Join(fx.RepoDir, ".claude", "jig.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decl := string(data) + "\nlight:\n  paths: [\"*.md\", \".github/*.md\", \"docs/**/*.md\"]\n  oracle: \"" + e2eLightOracle + "\"\n"
	writeFileOrFatal(t, path, []byte(decl))
}

// verifyWaiting verifies sha for slice in the foreground and returns the oracle
// line it journaled.
func verifyWaiting(t *testing.T, h *fakeHarness, fx *fixture.Fixture, sha, slice string) journal.Line {
	t.Helper()
	res := h.Verify(fx.Ticket, slice)
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify %s exit = %d, want a pass\nstdout:\n%s\nstderr:\n%s", sha, res.Code, res.Stdout, res.Stderr)
	}
	l, ok := oracleLine(readJournal(t, fx, fx.Ticket), slice, sha, "pass")
	if !ok {
		t.Fatalf("no passing oracle line for %s", sha)
	}
	return l
}

// TestVerifyRunsTheSlicesOwnOracleWhateverTheDelta: a Markdown-only commit
// after a whole-suite pass runs the slice's own oracle in full, light rule or
// not: `jig verify` substitutes nothing, so its oracle line names no light
// command and no earlier run it stands on.
func TestVerifyRunsTheSlicesOwnOracleWhateverTheDelta(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "clean-round"})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-light")
	h.Branch(fx.Branch)
	declareLightInClone(t, fx)
	full := h.CommitScenario("a", 1)
	h.Push(fx.Branch)

	fullLine := verifyWaiting(t, h, fx, full, "a")
	if fullLine.Ran != "" || fullLine.ReusedFrom != "" {
		t.Fatalf("the first oracle line = %+v, want the whole suite", fullLine)
	}

	notes := h.Commit("a", "notes", map[string]string{"README.md": "notes\n", "docs/adr/0100-x.md": "x\n"})
	h.Push(fx.Branch)
	again := verifyWaiting(t, h, fx, notes, "a")
	if again.Ran != "" || again.ReusedFrom != "" || again.Command != fullLine.Command || again.Seconds < 1 {
		t.Fatalf("the notes oracle line = %+v, want the slice's own oracle %q run in full", again, fullLine.Command)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "green" {
		t.Errorf("slice a state = %+v, %v, want green", ss, err)
	}
}

// TestLightDeltaWithOneNonLightFileRunsTheOracle: a commit with a Markdown file
// and one other file runs the slice's own oracle, as without a light rule.
func TestLightDeltaWithOneNonLightFileRunsTheOracle(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "clean-round"})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-mixed")
	h.Branch(fx.Branch)
	declareLightInClone(t, fx)
	full := h.CommitScenario("a", 1)
	h.Push(fx.Branch)
	verifyWaiting(t, h, fx, full, "a")

	mixed := h.Commit("a", "notes and a file", map[string]string{"README.md": "notes\n", "alpha/extra.txt": "x\n"})
	h.Push(fx.Branch)
	l := verifyWaiting(t, h, fx, mixed, "a")
	if l.Ran != "" || l.ReusedFrom != "" || l.Seconds < 1 {
		t.Errorf("oracle line = %+v, want the slice's own oracle run", l)
	}
}
