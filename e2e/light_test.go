package e2e

// End-to-end test of the light delta (GATE-18): a slice verified by the whole
// suite, a Markdown-only commit verified with the manifest's light oracle
// alone, and a gate that reuses the earlier whole-suite pass; and a delta with
// one non-light file, which runs the slice's oracle.

import (
	"encoding/json"
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
	res := h.Jig("verify", fx.Ticket, sha, "--slice", slice, "--wait")
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify %s exit = %d, want a pass\nstdout:\n%s\nstderr:\n%s", sha, res.Code, res.Stdout, res.Stderr)
	}
	l, ok := oracleLine(readJournal(t, fx, fx.Ticket), slice, sha, "pass")
	if !ok {
		t.Fatalf("no passing oracle line for %s", sha)
	}
	return l
}

// TestLightDeltaChainThroughTheBinary: the whole-suite pass of slice a stands
// for a later Markdown-only commit, which runs the lint alone and is green, and
// the gate reuses the whole-suite pass without running it, naming its origin
// and the light command.
func TestLightDeltaChainThroughTheBinary(t *testing.T) {
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
	light := verifyWaiting(t, h, fx, notes, "a")
	if light.Ran != e2eLightOracle || light.ReusedFrom != full || light.Command != fullLine.Command {
		t.Fatalf("the notes oracle line = %+v, want the light oracle reusing %s under the slice's command", light, full)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "green" {
		t.Errorf("slice a state = %+v, %v, want green", ss, err)
	}
	status := runJig(t, env, fx.StoreDir, "status", fx.Ticket)
	if want := "lint only, reusing " + full[:12]; !strings.Contains(status.Stdout, want) {
		t.Errorf("jig status lacks %q:\n%s", want, status.Stdout)
	}

	gate := runJig(t, env, fx.StoreDir, "gate", fx.Ticket, "--early")
	if gate.Code != 0 || !strings.Contains(gate.Stdout, "round: 1") {
		t.Fatalf("jig gate exit = %d, want round 1\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	var review struct {
		OraclesPassed []struct {
			Workspace  string `json:"workspace"`
			ReusedFrom string `json:"reused_from"`
			Ran        string `json:"ran"`
		} `json:"oracles_passed"`
	}
	data := readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "work", "gate.round-1.review.json"))
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("parse review.json: %v\n%s", err, data)
	}
	found := false
	for _, r := range review.OraclesPassed {
		if r.Workspace == "alpha" {
			found = true
			if r.ReusedFrom != full || r.Ran != e2eLightOracle {
				t.Errorf("alpha = %+v, want reused from %s with %q ran", r, full, e2eLightOracle)
			}
		}
	}
	if !found {
		t.Errorf("oracles_passed has no alpha row: %s", data)
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
